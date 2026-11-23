# Archive JSON nesting

Source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. This extension covers the nesting part of inventory history 36. It adds to the archive manifest and seen-path model.

Run `.agents/skills/verify-backup-restore/run-json` from the repository root. The command checks JSONNesting.tla, three broken configurations, real parser probes, three source faults, and restored current source. Product files stay unchanged.

The current model passes over 32 distinct states. It tracks the next input token, a stack of expected container ends, and reading, complete-value, accepted, or rejected states. Seven fixed inputs include a scalar, two nested arrays, an object with a nested array, three arrays above the limit, a truncated array, an empty array, and two scalar values. The nesting limit is two. Object keys and JSON token grammar are abstracted as successful decoder operations. The stack models the Go call stack used by consumeBackupJSONValue, consumeBackupJSONObject, consumeBackupJSONArray, and consumeNextBackupJSONValue. CheckEnd represents validateBackupCatalogReader's final EOF check.

| Rule | Broken model | Real source fault and required assertion |
| --- | --- | --- |
| DepthBound | Permit a third open container. | Raise depth limit from 128 to 256. The probe reports `excess JSON depth accepted`. |
| AcceptedComplete | Accept end of input with an open container. | Return before reading the closing array delimiter. The probe reports `nested JSON value not fully consumed`. |
| AcceptedComplete | Accept a trailing value. | Skip the final catalog EOF rejection. The probe reports `trailing JSON accepted`. |

Each broken model exits 12 with its named invariant. Each source fault fails the expected behavioral assertion. The first trailing-data fault caused an unused-variable compiler error. That attempt was rejected. The corrected fault retains the decoder error variable and reaches the required assertion. Both current-source runs pass.

The real probe consumes scalar, nested array, nested object, and empty array values. It checks complete consumption, truncated arrays and objects, actual depths 127 through 130, and trailing catalog JSON. This is a finite input family. It does not prove the encoding/json decoder, arbitrary JSON grammar, every object key, catalog schema validation, arbitrary nesting histories, or resource behavior. Decoder errors are rejection results. Objects' key consumption and value token retrieval are combined in the model.

Read the current and broken model results with `/statechart`, using JSONNestingView.js. Run it in a new scratch directory. Charts merge equal reader labels and hide steps that stay inside one label. They keep distinct marks for an incomplete accepted value, trailing accepted value, and excessive open containers.

The current chart has six labels and nine arrows. The three broken charts have seven labels and nine or ten arrows. All rules were checked. The negative charts retain distinct `breaks` states. None has a `mayBreak` mark.

- [Current chart](current/chart.mmd) and [report](current/report.md).
- [Depth control](brokendepth/chart.mmd) and [report](brokendepth/report.md).
- [Unfinished-input control](brokenend/chart.mmd) and [report](brokenend/report.md).
- [Trailing-value control](brokentrailing/chart.mmd) and [report](brokentrailing/report.md).

Model the Domain led to an explicit container stack and token position. Prove It Works led to the parser overlay checks and the final restored-current run.
