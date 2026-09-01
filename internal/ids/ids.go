// Package ids 生成不透明主键；不接链路中段。
package ids

import (
	"github.com/google/uuid"
)

// New 返回一个 UUID。
func New() string {
	return uuid.NewString()
}
