package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vatebur/gamehelm/internal/app"
	"github.com/vatebur/gamehelm/internal/config"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	check := flag.Bool("check", false, "只读校验配置及 systemd/Docker 目标")
	checkConfig := flag.Bool("check-config", false, "只读校验配置格式和认证参数")
	flag.Parse()
	var err error
	if *checkConfig {
		_, err = config.Load(*configPath)
	} else if *check {
		err = app.Check(*configPath)
	} else {
		err = app.Run(*configPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
