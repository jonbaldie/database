# Storage commit and restart check

Source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.

The combined verify-storage-recovery command completed with exit 0. TLC 1.7.4 checked 15 distinct current-model states. Both current rules hold. The missing-commit model breaks AcknowledgedRowsSurviveRestart. The eager-replay model breaks UncommittedRowsStayHidden.

Current source probes pass for private visibility, publication after commit, repeated commit rejection, close and reopen, empty commit after closure, repeated close, and incomplete-log recovery. Three temporary source overlays failed at named assertions.

- Replacing the final commit marker with begin loses the acknowledged row after restart.
- Removing the closed-engine guard accepts an empty commit after closure.
- Applying a pending operation immediately exposes an incomplete transaction after restart.

The delegated WAL model and real-code probes also pass. That model reached 1375 states. Its source probe opens all byte prefixes of a two-table replacement transaction and checks selected checkpoint and legacy-record cases.

The model treats complete records as atomic. The source restart test closes and reopens the engine. It does not kill a process between write steps. The delegated byte-prefix check tests interrupted log images, with its own stated limits. Neither check proves power-loss durability. Separate models cover the remaining storage histories. Arbitrary concurrent operations and all I/O failure combinations remain outside these finite checks.

The chart shows 10 labels and 15 arrows. Four stuttering or merged steps stay within one chart label. No cross-label transition is hidden. Replay arrows name the resulting row set. The merge of restart states does not show which log prefix caused each recovery result.

Run .agents/skills/verify-storage-recovery/run from the repository root. To regenerate the chart, copy CommitRecovery.tla, CommitRecovery.cfg, and CommitRecoveryView.js to a fresh directory outside the repo. Run /statechart there with a new output directory. Keep chart.mmd and report.md only.
