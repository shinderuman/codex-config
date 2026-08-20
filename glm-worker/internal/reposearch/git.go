package reposearch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// gitOutputはgit subprocessをcontext付きで実行しstdoutを返す。context取消時は
// kill後のexit errorではなくctx.Err()をそのまま返す。
func gitOutput(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...).Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}
