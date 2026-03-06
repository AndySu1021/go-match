package config

import (
	"go-match/pkg/questdb"
)

func InitQuestDBConfig() *questdb.QuestDBConfig {
	path := "conf"
	name := "questdb"
	configType := "yml"
	cfg := &questdb.QuestDBConfig{}
	Load(path, name, configType, cfg, false)

	return cfg
}
