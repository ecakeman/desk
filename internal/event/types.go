package event

// 事件类型是事实的名字；投影与索引按这些字符串分支。
const (
	TypeRunCreated       = "run.created"
	TypeMessageUser      = "message.user"
	TypeRunFailed        = "run.failed"
	TypeToolRequested    = "tool.requested"
	TypeToolStarted      = "tool.started"
	TypeToolCompleted    = "tool.completed"
	TypeRunCompleted     = "run.completed"
	TypeEpisodeCompacted = "episode.compacted"
	TypeMessageCompleted = "message.completed"
	TypeMessageDelta     = "message.delta"
	TypeToolDenied       = "tool.denied"
	TypeToolFailed       = "tool.failed"
	TypeRunInterrupted   = "run.interrupted"
	TypeTaskUpdated      = "task.updated"
	TypeSkillRevised     = "skill.revised"
	TypeSkillChallenge   = "skill.challenge"
	TypeSkillOverridden  = "skill.overridden"
	TypeMemoryRetrieved  = "memory.retrieved"
	TypePromptApplied    = "prompt.applied"
	TypeReviewCompleted  = "review.completed"
)
