package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vatebur/gamehelm/internal/app"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	check := flag.Bool("check", false, "校验配置和 user services")
	flag.Parse()
	var err error
	if *check {
		err = app.Check(*configPath)
	} else {
		err = app.Run(*configPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
