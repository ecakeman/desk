# Desk test artifacts

证据链：`command` → `raw/` → `normalized.json` → `metrics.json` → `report.md`。

```text
artifacts/
├── catalog.json
├── runs/<UTC-ts>/
│   ├── meta.json
│   ├── raw/
│   ├── normalized.json
│   ├── metrics.json
│   └── report.md
└── latest -> runs/<UTC-ts>
```

- **不要**把 API Key、Authorization、完整含密钥的 URL 写入任何 artifact。
- `raw/` 体积大，默认 gitignore。
- **13/13 Runtime Contracts** 只对应 `scripts/verify.sh` 里的 13 个 `run_contract`，见 `catalog.json` 的 `runtime_contracts`。不要把全部 `go test` 说成 13/13。
- Live showcase 与 deterministic contract **分指标**，禁止相加。

采集：

```text
make verify-artifacts-smoke   # 冒烟：schema + 单 contract
make verify-artifacts         # 正式：integration + 13 contracts + extra packs
make showcase-live-auto       # 真实模型；需 DESK_SHOWCASE_ARTIFACT 或脚本 --artifact-dir
```
