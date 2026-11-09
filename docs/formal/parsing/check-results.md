# Parser boundary checks

The checked source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Product files have no changes from this work.

The command .agents/skills/verify-parsing/run completed with exit 0. It ran TLC 1.7.4, current real-code probes, and temporary broken-source overlays. The negative runs count only when their named rule or behavioral assertion fails.

| Current model | Distinct states | Rules |
| --- | --- | --- |
| QuotedSQL | 15 | ClosingBoundary |
| DepthGuard | 16 | BoundedCallback, DepthRestored |
| CommandFlags | 42 | NoDuplicateSetting, NoEmptyValue |

All current rules hold. Each of the six broken models failed the expected rule. Removing SQL backslash or doubled-quote handling breaks ClosingBoundary. Removing the depth bound breaks BoundedCallback. Removing depth restoration breaks DepthRestored. Removing either command guard breaks its corresponding flag rule.

The current Go probe checked 16383 quoted-literal inputs. It enumerated all bodies of length zero through six over a four-byte alphabet for each of three quote delimiters. A separate finite-state recognizer supplied expected closing positions. It also checked selected comment markers, CSV nesting, string round trips, malformed tokens, shallow expression nesting, all 4002 initial depth values from zero through 4001, and command flag forms.

Nine source overlays failed at the expected assertions.

| Removed behavior | Observed failure |
| --- | --- |
| SQL backslash escape | Unterminated escaped quote accepted as a closing quote |
| SQL doubled quote | First half of a doubled quote accepted as a closing quote |
| Quoted comment protection | Literal containing a comment marker changed |
| CSV depth check | A comma inside parentheses split the input |
| Expression depth bound | Callback entered at caller depth 4000 |
| Expression depth unwind | Caller depth changed from zero to one |
| Regular duplicate flag guard | Repeated data-directory flag accepted |
| Config path duplicate guard | Repeated config flag accepted |
| Empty flag value guard | Empty data-directory value accepted |

The quote model uses four fixed abstract inputs. The depth model uses a limit of two and initial depths zero through three. The command model uses exactly two flags, two names, and empty or nonempty values. The real-code probes extend these model bounds as stated in the skill. They do not prove the complete grammar.

The charts contain 7, 6, and 5 arrows for quotes, depth, and flags. They omit only steps that stay within one label. The chart tool reports 4, 4, and 16 such steps. No cross-label transition is explicitly hidden. The quote chart merges positions with the same scanner action. It does not show every literal spelling. The command chart merges the two symbolic setting names.

Full statement grammar, all token classes, precedence, placeholders, arbitrary Unicode, malformed UTF-8, SQL modes, all commands, environment and file precedence, and every recursive parser entry remain outside this check. History 35 has partial bounded coverage.

To regenerate a chart, copy the matching .tla, .cfg, and View.js files from verify-parsing into a fresh temporary directory outside the repo. Run /statechart there with a new output directory. Retain chart.mmd and report.md only.
