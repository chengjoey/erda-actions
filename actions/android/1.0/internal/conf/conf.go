package conf

type Conf struct {
	WorkDir       string   `env:"WORKDIR"`
	Commands      []string `env:"ACTION_COMMANDS"`
	Target        string   `env:"ACTION_TARGET"`
	Targets       []string `env:"ACTION_TARGETS"`
	Context       string   `env:"ACTION_CONTEXT"`
	JDKVersion    string   `env:"ACTION_JDK_VERSION"`
	NexusUrl      string   `env:"BP_NEXUS_URL"`
	NexusUsername string   `env:"BP_NEXUS_USERNAME"`
	NexusPassword string   `env:"BP_NEXUS_PASSWORD"`
	PipelineID    string   `env:"PIPELINE_ID"`
}
