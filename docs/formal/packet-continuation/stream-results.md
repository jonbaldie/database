# Result stream results

At source baseline fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1, ResultStream passed over six states. The broken model failed ErrorDoesNotEndWithSuccess. Current source probes passed. Removing the return after writeStreamError failed the named assertion because an EOF success terminator followed the error.

Text and binary streams each emit one row, then SQL error 1105. The final packet must be the error. Malformed stored BIT data must emit only an error before metadata. A non-SQL stream error returns after metadata without a terminal packet. These tests use real StreamWriter and writeResult calls with memory buffers.

The initial preflight fixture used a malformed integer string in text mode. Source inspection showed that text-mode validation only parses BIT values. The fixture was corrected to malformed BIT data. This was a probe error, not a product defect.

Result streaming passes for these bounds. The overall packet runner deliberately returns 1 for the separate continuation finding. Its final outcome is PacketProtocol 12, Repaired 0, current source 1, repaired source 0, and restored-broken source 1. Both current and restored-broken source fail with packet continuation sequence mismatch. A failure earlier in the runner is not accepted as this finding.

The resultstream chart has six labels and seven arrows. It omits three same-label steps. Arbitrary metadata, all encodings, partial network writes, and concurrent transport are outside scope.
