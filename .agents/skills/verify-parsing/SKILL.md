---
name: verify-parsing
description: Check quoted SQL boundaries, expression recursion guards, and command flag rejection when changing string_literal.go, sql_comment.go, expression parsing, splitCSV, or configuration flag parsing. This skill covers selected parser boundaries, not the complete SQL grammar.
---

# Verify parser boundaries

Quoted SQL must close at an unescaped, undoubled delimiter. Expression recursion must stop at its limit and restore caller depth. Command flags must reject empty values and repeated settings.

If these rules fail, SQL can change meaning, expression parsing can exceed its stack limit, or the command can use an unintended setting.

Run from the repository root.

```sh
.agents/skills/verify-parsing/run
```

The command needs Python 3, Java 11 or later, and the Go toolchain in go.mod. Install the checksum-pinned TLC 1.7.4 jar with verify-wal-atomicity first if needed. TLA2TOOLS can name the same jar at another location. The runner limits Go and TLC to two processors. It uses temporary Go overlays and leaves product files unchanged.

Read each model result with `/statechart`. Use each model's .tla, .cfg, and View.js files in this skill. Keep a model failure visible. A broken model or source overlay must fail at its expected rule or assertion. A compiler error does not count as a caught break.

## Source and sync

Last confirmed source commit is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Before use, inspect later commits and uncommitted changes to the functions below. Move the sync commit forward only after the models and probes describe the changed source.

| Check | Source functions |
| --- | --- |
| QuotedSQL.tla and exhaustive quote probe | internal/mysql/string_literal.go, quotedSQLLiteralEnd, escapedSQLCharacter, doubledSQLQuote |
| CommentModes.tla, ListNesting.tla, and comment/CSV/string probes | internal/mysql/sql_comment.go, stripSQLComments and consumeSQLQuoted; internal/mysql/server.go, splitCSV; internal/mysql/expression_token.go, tokenizeExpression and scanString |
| DepthGuard.tla and depth probe | internal/mysql/expression_parse.go, depthGuard and parseExpression |
| CommandFlags.tla and flag probes | cmd/database/config.go, parseConfigurationFlags, configurationFlagValue, checkedConfigurationFlagValue, configurationFlagParser.add and setPath |

## Bounds and evidence

QuotedSQL.tla explores four fixed abstract literals. They include a backslash-escaped quote, a doubled quote, a simple closing quote, and an unterminated escaped quote. Complete scanner steps can consume one or two bytes. The model does not describe the whole lexer.

The real quote probe checks all bodies of length zero through six over four bytes. The alphabet contains a letter, the current quote, a backslash, and a comma. It runs separately for single quotes, double quotes, and backticks. This gives 16383 scanner inputs. A separate finite-state recognizer provides expected closing positions. Backticks do not use backslash escapes.

Selected generated literals also test comment preservation, CSV separation with nested parentheses, and token decode round trips. Selected malformed numbers and literals must fail tokenization. Parenthesis nesting from zero through eight must parse the value one and restore depth. These are probes, not a proof of the grammar.

DepthGuard.tla uses a depth limit of two and caller depths zero through three. The source probe checks every caller depth from zero through maxExpressionDepth plus one. It calls depthGuard directly. It does not construct thousands of nested parser calls.

CommandFlags.tla checks two flags from two symbolic names and either an empty or nonempty value. The source probe uses data-directory, max-connections, and config flags with joined and separate values. It checks duplicates across both forms, missing and empty values, and selected unknown names. Flag normalization is synchronous, so the model treats one consume operation as one step.

Eight broken models must fail. Nine source overlays remove SQL escape or doubled-quote handling, quoted comment protection, CSV nesting, the recursion bound, depth restoration, regular duplicate rejection, config-path duplicate rejection, or empty-value rejection. Each must fail at its named behavioral assertion.

## Limits and maintenance

History 35 has bounded coverage of these parser states. Full statement grammar, all token classes, operator precedence, prepared placeholders, arbitrary Unicode, malformed UTF-8, SQL modes, every command, configuration environment and file precedence, and all parser entry points remain outside this skill. Successful tokenization does not prove statement acceptance.

If a model failure does not reproduce against source, inspect the model first. If a source change is intentional, update the model and probe together. Show the property failing on a corresponding broken source overlay. Run this skill and `/statechart` again. Use `/maintain-formal-verification-skill` when covered source changes without these checks.

CommentModes follows six abstract tokens through SQL and block-comment modes. A quoted token is atomic and retains its comment marker. ListNesting follows seven tokens through nested parentheses and splits only at the outer comma. Their broken controls remove quoted-token preservation or depth tracking. The existing quoted-comment and CSV source faults are the corresponding real-code controls. These models assume quoted-token scanning, which QuotedSQL checks separately.
