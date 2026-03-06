package main

import (
	"go-match/config"
	"go-match/pkg/nats"
	"go-match/pkg/questdb"
)

type Config struct {
	App     *config.AppConfig
	Nats    *nats.NatsConfig
	QuestDB *questdb.QuestDBConfig
}

var cfg *Config
