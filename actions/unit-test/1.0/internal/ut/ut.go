package ut

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/erda-project/erda-actions/actions/unit-test/1.0/internal/base"
	_go "github.com/erda-project/erda-actions/actions/unit-test/1.0/internal/parser/go"
	"github.com/erda-project/erda-actions/actions/unit-test/1.0/internal/parser/java"
	"github.com/erda-project/erda-actions/actions/unit-test/1.0/internal/parser/js"
	"github.com/erda-project/erda-proto-go/dop/qa/unittest/pb"
	"github.com/erda-project/erda/apistructs"
	"github.com/erda-project/erda/pkg/filehelper"
	"github.com/erda-project/erda/pkg/http/httpclient"
	"github.com/erda-project/erda/pkg/metadata"
	"github.com/erda-project/erda/pkg/qaparser"
)

type Ut struct{}

func NewUt() *Ut {
	return &Ut{}
}

func (ut *Ut) UnitTest() error {
	var (
		path      string
		suites    []*pb.TestSuite
		report    []*pb.CodeCoverageNode
		language  string
		suite     *pb.TestSuite
		err       error
		qaID      string
		utResults *pb.TestCallBackRequest
	)

	context := base.Cfg.Context
	if context == "" {
		path = "."
		if language, err = checkLanguage(path); err != nil {
			return err
		}

		logrus.Infof("start to execute test, language:%s", language)
		if suite, report, err = executeTest(language, path); err != nil {
			return err
		}

		suites = append(suites, suite)
	} else {
		for _, p := range strings.Split(context, ",") {
			p = strings.Trim(p, " ")
			if err = os.Chdir(filepath.Dir(base.Cfg.WorkDir)); err != nil {
				return errors.Wrapf(err, "failed to change directory, path: %s", filepath.Dir(base.Cfg.WorkDir))
			}

			if language, err = checkLanguage(p); err != nil {
				return err
			}

			logrus.Infof("start to execute test, language:%s", language)
			if suite, report, err = executeTest(language, p); err != nil {
				return err
			}

			suites = append(suites, suite)
		}
	}

	if utResults, err = makeUtResults(suites); err != nil {
		return err
	}
	utResults.CoverageReport = report

	if qaID, err = callback(utResults); err != nil {
		logrus.Errorf("failed to callback, (%+v)", err)
		return err
	}

	return storeMetaFile(qaID, utResults)
}

func executeTest(language, path string) (*pb.TestSuite, []*pb.CodeCoverageNode, error) {
	switch language {
	case base.Java:
		return java.MavenTest(path)
	case base.Js:
		return js.JsTest(path)
	case base.Golang:
		return _go.GoTest(path)
	}

	return nil, nil, errors.Errorf("not support, language: %s", language)
}

func checkLanguage(path string) (string, error) {
	var (
		buildPack Buildpack
		err       error
	)

	if buildPack, err = DetectBuildPack(path); err != nil {
		return "", errors.Wrapf(err, "failed to detect buildPack")
	}

	switch buildPack.Language {
	case base.Java, "kotlin":
		return base.Java, nil
	case "dice_spa", "herd", "javascript":
		return base.Js, nil
	case base.Golang, "go":
		return base.Golang, nil
	}

	return buildPack.Language, nil
}

func calculateTotals(suites []*pb.TestSuite, totals *pb.TestCallBackRequest) {
	if totals.Totals == nil {
		totals.Totals = &pb.TestTotal{
			Statuses: make(map[string]int64),
		}
	}

	for _, s := range suites {
		to := &qaparser.Totals{totals.Totals}
		totals.Totals = to.Add(s.Totals).TestTotal
	}
}

// callback to qa
func callback(req *pb.TestCallBackRequest) (string, error) {
	var result = struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
		Err     struct {
			Code    string                 `json:"code,omitempty"`
			Message string                 `json:"msg,omitempty"`
			Ctx     map[string]interface{} `json:"ctx,omitempty"`
		} `json:"err,omitempty"`
	}{}

	resp, err := httpclient.New(httpclient.WithCompleteRedirect()).
		Post(base.Cfg.DiceOpenapiPrefix).Path("/api/qa/actions/test-callback").
		Header("Authorization", base.Cfg.DiceOpenapiToken).
		Header("Content-Type", "application/json").
		JSONBody(&req).Do().JSON(&result)

	if err != nil {
		return "", errors.Wrapf(err, "failed to report results to qa, req: %+v", req)
	}

	if !resp.IsOK() {
		return "", errors.Errorf("failed to report results to qa, code: %d, req: %+v, result: %+v",
			resp.StatusCode(), req, result)
	}

	// 一般不会发生
	if result.Err.Code != "" {
		return "", errors.Errorf("failed to report results to qa, (%+v)", result.Err)
	}

	logrus.Infof("successed to report results to qa, req: %+v, qaID: %s", req, result.Data)

	return result.Data, nil
}

func makeUtResults(suites []*pb.TestSuite) (*pb.TestCallBackRequest, error) {
	results := &pb.TestCallBackRequest{
		Results: &pb.TestResult{
			Extra: make(map[string]string),
		},
		Totals: &pb.TestTotal{
			Statuses: make(map[string]int64),
		},
	}

	results.Suites = suites

	calculateTotals(suites, results)

	if err := base.ComposeResults(results.Results); err != nil {
		return nil, err
	}

	return results, nil
}

// calculateTestCoverage calculate test rate, return passedRate, failedRate, skippedRate
func calculateTestCoverage(totals *pb.TestCallBackRequest) (float64, float64, float64) {
	var (
		total       int64
		passedRate  float64
		failedRate  float64
		skippedRate float64
	)
	total = totals.Totals.Statuses[string(apistructs.TestStatusPassed)] +
		totals.Totals.Statuses[string(apistructs.TestStatusFailed)] +
		totals.Totals.Statuses[string(apistructs.TestStatusSkipped)] +
		totals.Totals.Statuses[string(apistructs.TestStatusError)]
	if total == 0 {
		passedRate = 0
		failedRate = 0
		skippedRate = 0
	} else {
		passedRate = float64(totals.Totals.Statuses[string(apistructs.TestStatusPassed)]) / float64(total) * 100
		failedRate = float64(totals.Totals.Statuses[string(apistructs.TestStatusFailed)]+
			totals.Totals.Statuses[string(apistructs.TestStatusError)]) / float64(total) * 100
		skippedRate = float64(totals.Totals.Statuses[string(apistructs.TestStatusSkipped)]) / float64(total) * 100
	}
	return passedRate, failedRate, skippedRate
}

func storeMetaFile(qaID string, req *pb.TestCallBackRequest) error {
	passedRate, failedRate, skippedRate := calculateTestCoverage(req)
	meta := apistructs.ActionCallback{
		Metadata: metadata.Metadata{
			{
				Name:  "projectId",
				Value: strconv.FormatUint(base.Cfg.ProjectID, 10),
			},
			{
				Name:  "AppId",
				Value: strconv.FormatUint(base.Cfg.AppID, 10),
			},
			{
				Name:  "operatorId",
				Value: base.Cfg.OperatorID,
			},
			{
				Name:  "commitId",
				Value: base.Cfg.GittarCommit,
			},
			{
				Name:  apistructs.ActionCallbackQaID,
				Value: qaID,
				Type:  apistructs.ActionCallbackTypeLink,
			},
			{
				Name:  "passedRate",
				Value: fmt.Sprintf("%.2f", passedRate),
			},
			{
				Name:  "failedRate",
				Value: fmt.Sprintf("%.2f", failedRate),
			},
			{
				Name:  "skippedRate",
				Value: fmt.Sprintf("%.2f", skippedRate),
			},
		},
	}

	// 上报元信息，后续交给 pipeline 上报
	if req != nil {
		resultJson, err := json.Marshal(req.Results)
		if err != nil {
			return err
		}
		meta.Metadata = append(meta.Metadata, metadata.MetadataField{
			Name:  "results",
			Value: string(resultJson),
		})

		suitesJson, err := json.Marshal(req.Suites)
		if err != nil {
			return err
		}
		meta.Metadata = append(meta.Metadata, metadata.MetadataField{
			Name:  "suites",
			Value: string(suitesJson),
		})

		totalsJson, err := json.Marshal(req.Totals)
		if err != nil {
			return err
		}
		meta.Metadata = append(meta.Metadata, metadata.MetadataField{
			Name:  "totals",
			Value: string(totalsJson),
		})
	}

	b, err := json.Marshal(&meta)
	if err != nil {
		return err
	}
	if err := filehelper.CreateFile(base.Cfg.MetaFile, string(b), 0644); err != nil {
		return errors.Wrap(err, "write file:metafile failed")
	}
	return nil
}
