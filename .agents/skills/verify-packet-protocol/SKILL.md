---
name: verify-packet-protocol
description: Check MySQL packet continuation acceptance and result-stream error termination when changing frame handling or StreamWriter.
---

# Verify packet continuation

Rule: A valid message emitted by StreamWriter must be accepted by readPacket, with its payload and final sequence unchanged.
Harm: A client cannot send a large command when a valid continuation is rejected.

Run `.agents/skills/verify-packet-protocol/run` from the repository root. It needs Python 3, Java, Go from go.mod, and the checksum-pinned TLC 1.7.4 jar installed by verify-wal-atomicity. It runs one TLC worker and at most two Go processors. Exit 1 is the current finding, not a passing check.

Read each TLC result with `/statechart`. Use PacketProtocol.tla, PacketProtocol.cfg, and PacketProtocolView.js here. Use Repaired.cfg to inspect the proposed sequence behavior. Follow the installed `/statechart` instructions for its command.

## Coverage and sync

Confirmed at fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. History 25 in docs/formal/coverage.md belongs to this subsystem. This check covers frame sequencing and a bounded result-stream termination rule.

- internal/mysql/stream_writer.go, StreamWriter.writeRawPacket increments a byte sequence after each emitted frame.
- readPacket reads the first sequence and increments expected again after each header. ReadFirstHeader abstracts that local calculation; ReadValidContinuation abstracts the following comparison.
- packet_test.go.txt runs these real functions through a Go overlay. It tests start values 0, 254, and 255 and payload sizes one below, at, and one above the full frame size. It checks bytes and final sequence.

The TLA+ model covers two valid frames and the same three sequence starts. It assumes complete reads, sufficient packet limit, and no transport failure. It leaves out malformed packets, discard behavior, result metadata, TLS, and arbitrary message lengths. A model pass covers only this bound.

## Findings and maintenance

Current path: read the first full frame; receive the next consecutive frame; reject it because expected was advanced twice. The real-code probe must reproduce the model path. The repaired overlay removes the extra first-frame increment and must pass. The broken overlay restores it and must fail. These overlays never change the working tree.

Before a change, inspect the covered source since the sync commit, including working changes. Update the model and probe when the protocol changes. Keep the acceptance rule if it fails. Advance the sync commit only after source review and the real probe agree. Use `/maintain-formal-verification-skill` after changes that did not run this check.

## Result streaming

The same command first runs run-stream. Read ResultStream.tla, ResultStream.cfg, ResultStreamBroken.cfg, and ResultStreamView.js with `/statechart`. The current model has six states. It separates stored-result validation, column metadata, row delivery, success, SQL error, and transport failure. A row-stream SQL error must end with an error packet, without a later success terminator.

The source probe calls StreamWriter.WriteResultSet and writeResult. It checks text and binary delivery of one row followed by SQL error 1105. The final packet must be the error. A malformed stored BIT value must produce one error packet before metadata. A non-SQL stream error must return the error after metadata without an extra terminal packet. Removing the return after writeStreamError must fail the named terminator assertion. The broken model must violate ErrorDoesNotEndWithSuccess.

These probes use memory buffers and bounded rows. Full encodings, malformed frames, partial network writes, arbitrary metadata, and transport concurrency remain outside scope. The result-stream checks pass independently of the current continuation finding. A failure before the final continuation outcome is a check failure, not the expected finding.

The current continuation defect is tracked in [issue #608](https://github.com/jonbaldie/database/issues/608).
