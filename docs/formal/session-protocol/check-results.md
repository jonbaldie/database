# Session protocol checks

Source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.

Run .agents/skills/verify-session-protocol/run from the repository root. The combined runner checks sixteen current TLA+ models, twenty-five broken model configs, current Go probes, and twenty-six broken-source overlays. Each source mutation must fail a named assertion. Compiler failures do not pass a negative check. The seven added sequence models and their controls are recorded in [history-results.md](history-results.md).

The results below describe the original nine boundary and connect-execute models. They remain part of the combined command.

The complete command passed with exit 0. The current models passed at these bounds.

| Model | Distinct states |
| --- | --- |
| Admission | 5 |
| Handshake | 16 |
| Cancellation | 3 |
| Prepared | 3 |
| Settings | 14 |
| AccountGrant | 4 |
| Diagnostics | 3 |
| LiveExplanation | 5 |
| ConnectExecute | 11 |

All ten broken-model configs failed their intended invariants. All ten source mutations failed their named assertions. The current source probes passed. These counts describe the explored bounded graphs, not a measure of product coverage.

The boundary checks cover selected parts of histories 14, 15, 16, 19, 20, 21, 23, and 24. Each model follows the source functions named in the skill. The source probes call those real functions. They do not replace the functions with a separate implementation.

The connect-and-execute probe uses a local net.Pipe and calls runStatement for SELECT 1. During the callback, cancellation and live explanation are available. After return, the running flag, statement cancellation channel, resources, and live explanation are cleared. Its negative overlays remove the session cancellation channel and retain the running flag after return.

Other source mutations remove session capacity, omit a required handshake flag, close the same cancellation watch twice, retain long-data byte accounting after reset, remove a byte-limit ceiling, omit grant revocation, alias caller diagnostics, and remove a newer explanation during stale cleanup.

The models are bounded abstractions of selected transitions. They do not check arbitrary session histories or concurrent schedules. Authentication outcomes in the connect-and-execute chart are inputs. The added sequence checks exercise full password transport over TLS and RSA, transaction idle timeout, drain waiting, selected prepared decoding, persistent account updates, SET prefix retention, and warning retention. Their bounds are in history-results.md. Arbitrary transport failures, query-result streaming, shutdown schedules, all prepared encodings, all settings, and concurrent live metrics remain outside these checks.

The views retain each cross-label transition. Stuttering steps do not produce arrows. Model report directories contain current and broken charts. The broken chart paths are negative controls, not findings in current product code.

To regenerate a chart, copy its TLA file, chosen config, and matching View.js file to a new scratch directory outside the repo. Run /statechart there with a fresh output directory. Retain only chart.mmd and report.md.

The final restored-current source run passed after all mutations. The official skill validator passed with PyYAML supplied by uv. No production source file changed. No owned artifact exceeds 100 MB. Temporary logs and chart work directories were removed.
