// glm-workerはGLM Coding Plan上でSol Highと協調する永続実装ワーカーCLI。
// 本ファイルは薄いentrypointであり、実装は internal 配下のpackageへ委譲する。
package main

import (
	"fmt"
	"os"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
