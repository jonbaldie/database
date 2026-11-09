# Packet continuation check

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.
Tool: TLC 1.7.4, one worker. Go uses GOMAXPROCS=2 and GOFLAGS=-p=2.

| Check | Result |
| --- | --- |
| Current bounded model | Fails ValidMessageAccepted; TLC exit 12 |
| Repaired bounded model | Pass; TLC exit 0 |
| Current Go reader and writer | Fail on full-frame and full-frame-plus-one payloads at starts 0, 254, 255 |
| Repaired source overlay | Pass all nine start/size cases |
| Broken source overlay | Fail the same six continuation cases |

The model has nine reachable states. The shortest failure path has two actions: read the first full frame, then receive the next valid frame. The real-code test replays these actions through StreamWriter and readPacket. The chart hides only steps that retain the same chart label, including terminal stuttering.

The repair is a scratch overlay. Product source was not changed. Run `.agents/skills/verify-packet-protocol/run` to repeat the checks. It returns 1 while the current finding remains. Follow `/statechart` to regenerate the chart with the model, config, and view in that skill and a new scratch output directory.

This check covers packet sequence acceptance. [Result-stream checks](stream-results.md) cover the other modeled stages of history 25. See [coverage.md](../coverage.md) for finite limits.

The current continuation defect is tracked in [issue #608](https://github.com/jonbaldie/database/issues/608).
