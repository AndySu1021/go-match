package config

type Mode string

const (
	ModeGrpc  Mode = "grpc"
	ModeHttp  Mode = "http"
	ModeEvent Mode = "event"
)

type AppConfig struct {
	Env         string   `mapstructure:"env"`
	Instruments []string `mapstructure:"instruments"`
	Net         Net      `mapstructure:"net"`
	Snapshot    Snapshot `mapstructure:"snapshot"`
	WAL         WAL      `mapstructure:"wal"`
	Consumer    Consumer `mapstructure:"consumer"`
}

type Net struct {
	Addr string `mapstructure:"addr"`
}

type Snapshot struct {
	Dir    string `mapstructure:"dir"`
	Period int    `mapstructure:"period"`
}

type WAL struct {
	Dir string `mapstructure:"dir"`
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
