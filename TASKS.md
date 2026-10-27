# 任务清单与执行证据

## 已完成
- [x] 固定基线并在独立 worktree 应用额度候选。
- [x] `git diff --check`、`gofmt -l`、Windows `go vet ./...` 通过。
- [x] 定向额度/provider/gateway 测试通过。
- [x] 定向测试 `-race -count=5` 通过。
- [x] 补强真实主 Key/备用 Key 计数夹具：主触线时只走备用；全部触线时上游计数为零。
- [x] 增加旧 Provider 来源晚到读数回归测试；先失败，修复来源摘要后通过。
- [x] 变异闸门验证：移除 `CheckQuotaGuard` 后计数测试失败，证明测试有效。

## 当前证据
- [ ] 图像、视频、转录、rerank、压缩、决策和 Kimi/远程搜索出口的本地拒绝分类闭包：未完成；这些路径部分经过 `Provider.Do`，但还没有各自的 quota error 计数/健康状态证据。
- [ ] 全仓 `go test`：运行，非额度相关环境包失败（agent、cli、davsync、sessions、sqlitedb、testenv、usage）；额度/provider/gateway/backup 相关结果通过。
- [x] Windows amd64 production 桌面候选构建到 `G:\\temp\\magpie-build\\desktop-quota-20261027`；SHA256 已记录在本轮日志。
- [ ] GUI/PC/真实额度验证：未完成。
- [ ] 备份/恢复/同步生命周期：未完成。
- [ ] issue/PR、提交、推送：未执行。

## 下一步
- [ ] 完成出口矩阵和计费调用计数。
- [ ] 完成备份恢复边界与订阅旧语义审查。
- [ ] 全仓构建、测试和桌面试用后再准备 Draft PR。



