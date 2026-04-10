package config

type Mode string

const (
	ModeGrpc  Mode = "grpc"
	ModeHttp  Mode = "http"
	ModeEvent Mode = "event"
)

type AppConfig struct {
	Env        string   `mapstructure:"env"`
	Instrument string   `mapstructure:"instrument"`
	Mode       Mode     `mapstructure:"mode"`
	Net        Net      `mapstructure:"net"`
	Snapshot   Snapshot `mapstructure:"snapshot"`
	Consumer   Consumer `mapstructure:"consumer"`
}

type Net struct {
	Addr string `mapstructure:"addr"`
}

type Snapshot struct {
	Path   string `mapstructure:"path"`
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
