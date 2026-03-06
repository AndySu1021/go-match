package config

import (
	"fmt"
	"log/slog"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

func Load(configDir, filename, configType string, config interface{}, changeReloadEnable bool) {
	v := viper.New()
	v.SetConfigName(filename)
	v.SetConfigType(configType)
	v.AddConfigPath(configDir)
	if err := v.ReadInConfig(); err != nil {
		fmt.Println(err)
		return
	}
	if err := v.Unmarshal(&config); err != nil {
		fmt.Println(err)
		return
	}
	if changeReloadEnable {
		v.WatchConfig()
		v.OnConfigChange(func(e fsnotify.Event) {
			slog.Info("Config file changed:", e.Name)
			if err := v.Unmarshal(&config); err != nil {
				slog.Error("fail to decode config: ", err.Error())
			}
			slog.Info("config: ", config)
		})
	}
}
