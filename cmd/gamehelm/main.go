package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vatebur/gamehelm/internal/app"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	printInstallValues := flag.Bool("print-install-values", false, "输出安装模板所需的非敏感配置")
	flag.Parse()
	if err := app.Run(*configPath, *printInstallValues); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
