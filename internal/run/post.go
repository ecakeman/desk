// Package run 编排一句用户消息对应的一次 Run。
//
// HTTP 入口是 PostUserMessage：事务里建 runs 行并追加 run.created / message.user，
// 再用脱离请求的 Context 拉起 Drive。只有这条 goroutine 推进 Run。
// 事实写 events；runs 只保存当前状态。批准等待与 cancel 只在进程内。
package run

import (
	"context"
	"database/sql"
	"sync"

	"desk/internal/config"
	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/memory"
	"desk/internal/plugin"
	"desk/internal/session"
	"desk/internal/worker"
)

// Service 是控制面编排器：建 Run、Drive、批准、取消、终态。
type Service struct {
	DB         *sql.DB
	Events     *event.Store
	Plugins    *plugin.Registry
	Worker     worker.Worker
	Flash      config.ModelConfig
	Pro        config.ModelConfig
	Index      *memory.Index
	Sessions   *session.Store
	PromptsDir string

	mu      sync.Mutex
	pending map[string]*pendingApproval
	cancels map[string]context.CancelFunc
}

// NewService 只绑 DB 和 EventStore；插件、Worker、槽位由 serve 注入。
func NewService(db *sql.DB, events *event.Store) *Service {
	return &Service{
		DB:       db,
		Events:   events,
		Sessions: session.NewStore(db),
		pending:  map[string]*pendingApproval{},
		cancels:  map[string]context.CancelFunc{},
	}
}

// PostUserMessage 提交后立即返回 run_id；Drive 在后台跑。
func (s *Service) PostUserMessage(ctx context.Context, sessionID, text, workspace string) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if _, err := s.Sessions.GetTx(ctx, tx, sessionID); err != nil {
		return "", err
	}

	runID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,'running',$3)`,
		runID, sessionID, workspace,
	); err != nil {
		return "", err
	}
	if _, err := s.Events.Append(ctx, tx, runID, event.TypeRunCreated, map[string]string{
		"session_id": sessionID,
	}); err != nil {
		return "", err
	}
	if _, err := s.Events.Append(ctx, tx, runID, event.TypeMessageUser, map[string]string{
		"text": text,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if s.Worker != nil && s.Plugins != nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.cancels[runID] = cancel
		s.mu.Unlock()
		go func() {
			defer func() {
				s.mu.Lock()
				delete(s.cancels, runID)
				s.mu.Unlock()
			}()
			if err := s.Drive(ctx, runID); err != nil {
				if ctx.Err() != nil {
					_ = s.Interrupt(context.Background(), runID, "canceled")
					return
				}
				_ = s.Fail(context.Background(), runID, err.Error())
			}
		}()
	}
	return runID, nil
}
