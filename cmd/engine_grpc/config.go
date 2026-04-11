package main

import (
	"go-match/config"
	"go-match/pkg/nats"
)

type Config struct {
	App  *config.AppConfig
	Nats *nats.NatsConfig
}

var cfg *Config
