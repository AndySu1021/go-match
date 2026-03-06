package config

import "go-match/pkg/nats"

func InitNatsConfig() *nats.NatsConfig {
	path := "conf"
	name := "nats"
	configType := "yml"
	cfg := &nats.NatsConfig{}
	Load(path, name, configType, cfg, false)

	return cfg
}
