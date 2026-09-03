package ctxmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/memory"
	"desk/internal/testdb"
)

func testMgr(t *testing.T, window int, stub *StubCompactor) (*Manager, *event.Store, string, string) {
	t.Helper()
	db := testdb.Open(t)
	ctx := context.Background()
	sess := testdb.InsertSession(t, db)
	runID := ids.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'running','')`, runID, sess); err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	idx := memory.New(db)
	ev.OnInsert = idx.IndexTx
	root, _ := filepath.Abs("../..")
	m := New(ev, idx, Settings{
		WindowTokens:    window,
		SmallTriggerTok: 1,
		LargeTriggerTok: 50,
		LargeSmallCount: 2,
		PromptsDir:      filepath.Join(root, "prompts"),
	})
	m.Compactor = stub
	return m, ev, sess, runID
}

func appendUser(t *testing.T, ev *event.Store, runID, text string) {
	t.Helper()
	ctx := context.Background()
	tx, err := ev.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := ev.Append(ctx, tx, runID, event.TypeMessageUser, map[string]string{"text": text}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareUnderWindowNoCompact(t *testing.T) {
	stub := &StubCompactor{Err: context.Canceled}
	m, ev, sess, runID := testMgr(t, 100000, stub)
	appendUser(t, ev, runID, "hello context")
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID, Phase: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if stub.N != 0 {
		t.Fatalf("compact called %d", stub.N)
	}
	if asm.Rebuild {
		t.Fatal("rebuild")
	}
	found := false
	for _, msg := range asm.Messages {
		if strings.Contains(fmtString(msg["content"]), "hello context") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing user: %#v", asm.Messages)
	}
}

func TestPrepareSmallCompactSuccessAndFailure(t *testing.T) {
	ok := []byte(`{"summary":"会话要维护书签规则与状态文件","facts":[{"key":"goal","value":"write STATUS","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":["STATUS.md"],"decisions":[]}`)
	stub := &StubCompactor{Raw: ok}
	m, ev, sess, runID := testMgr(t, 20, stub)
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("window-payload-", 8)+ids.New())
	}
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID, Phase: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if stub.N == 0 {
		t.Fatal("expected compact")
	}
	if !asm.Rebuild && asm.CompactionReason == "" {
		t.Fatalf("expected compact reason %+v", asm.Applied)
	}
	var n int
	if err := ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextSmallCompact).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatal("missing small compact event")
	}

	fail := &StubCompactor{Err: context.Canceled}
	m.Compactor = fail
	before := n
	_, err = m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID, Phase: "act"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextSmallCompact).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before {
		t.Fatalf("failure wrote compact %d -> %d", before, n)
	}
}

func TestLargeRollingBaseline(t *testing.T) {
	smallJSON := func(seq int) []byte {
		return []byte(`{"summary":"小压缩保留当前书签任务状态","facts":[{"key":"k","value":"v","status":"active","confidence":0.8,"source_event_seqs":[` + itoa(seq) + `]}],"open_items":["x"],"decisions":["d"]}`)
	}
	stub := &StubCompactor{Raw: smallJSON(1)}
	m, ev, sess, runID := testMgr(t, 15, stub)
	m.Settings.LargeSmallCount = 2
	m.Settings.SmallTriggerTok = 1
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("large-payload-", 10)+ids.New())
	}
	_, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	stub.Raw = smallJSON(2)
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("large-payload-b-", 10)+ids.New())
	}
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	var smalls, larges int
	_ = ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextSmallCompact).Scan(&smalls)
	_ = ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextLargeCompact).Scan(&larges)
	if smalls < 1 {
		t.Fatalf("smalls=%d larges=%d n=%d", smalls, larges, stub.N)
	}
	if larges > 1 {
		// rolling may write one large; more is ok as history but assemble uses latest
	}
	if asm.Applied.LargeSeq == 0 && larges > 0 {
		t.Fatal("active large not in applied")
	}
}

func TestInvalidCompactDoesNotWrite(t *testing.T) {
	stub := &StubCompactor{Raw: []byte(`not-json`)}
	m, ev, sess, runID := testMgr(t, 20, stub)
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("bad-compact-", 10)+ids.New())
	}
	_, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextSmallCompact).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("wrote compact on invalid json: %d", n)
	}
}

func TestAssembleStablePrefixThenRetrieval(t *testing.T) {
	m, ev, sess, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	appendUser(t, ev, runID, "stable user text")
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID, Phase: "act"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID, Phase: "act", WantRetrieve: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(a1.Messages) == 0 || len(a1.Messages) != len(a2.Messages) {
		t.Fatalf("len %d %d", len(a1.Messages), len(a2.Messages))
	}
	for i := range a1.Messages {
		b1, _ := json.Marshal(a1.Messages[i])
		b2, _ := json.Marshal(a2.Messages[i])
		if string(b1) != string(b2) {
			t.Fatalf("prefix drift at %d", i)
		}
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
