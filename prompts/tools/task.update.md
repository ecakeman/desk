在当前 Run 中创建或更新一项可验证任务。非平凡请求先用 `open` 建立少量任务，完成后用同一 `id` 标记 `done`、`failed` 或 `skipped`；任务失败只记录事实，不等于 Run 失败。简单的一步回答不要创建任务。若任务实际采用了注入的 skill，把其 `path@version` 原样填入 `skill_ref`。
