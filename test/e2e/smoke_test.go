//go:build e2e

package e2e

import (
	"testing"

	"github.com/wanpengxie/go-kimi/pkg/kimi"
)

func TestE2ESmoke(t *testing.T) {
	if kimi.Version == "" {
		t.Fatal("kimi.Version must not be empty")
	}
}
