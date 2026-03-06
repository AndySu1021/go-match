package config

type AppConfig struct {
	Env        string   `mapstructure:"env"`
	Instrument string   `mapstructure:"instrument"`
	Snapshot   Snapshot `mapstructure:"snapshot"`
	Consumer   Consumer `mapstructure:"consumer"`
}

type Snapshot struct {
	Dir    string `mapstructure:"dir"`
	Period int    `mapstructure:"period"`
}

type Consumer struct {
	Stream string `mapstructure:"stream"`
}

func InitAppConfig() *AppConfig {
	path := "conf"
	name := "app"
	configType := "yml"
	cfg := &AppConfig{}
	Load(path, name, configType, cfg, false)

	return cfg
}
