# Backup and restore check results

Baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.
Command: `.agents/skills/verify-backup-restore/run` from the repository root.
The runner completed with exit 0. TLC is checksum-pinned to 1.7.4, with one worker. Go uses GOMAXPROCS=2 and GOFLAGS=-p=2.

| Check | Result |
| --- | --- |
| Restore model | Pass; 20 states; three invariants and deadlock checked |
| Archive entry model | Pass; 20 states; manifest rule and deadlock checked |
| Broken restore validation | Fails OnlyValidatedFilesInstalled |
| Broken manifest requirement | Fails ResultRequiresManifest |
| Current Go probes | Pass |
| Removed manifest guard | Fails missing manifest assertion |
| Removed file hash guard | Fails hash mismatch assertion |
| Ignored staged validation failure | Fails invalid stage assertion |
| Raised JSON depth limit | Fails excess depth assertion |

The real probes create an initialized source, make a backup, restore it into a new and an existing empty directory, and reload the new identity. They reject invalid staging, missing manifest, duplicate file and manifest entries, unsafe paths, wrong hashes, and trailing JSON. Generated JSON depths range from 0 through 130 and check the 128-depth boundary. A failed archive write preserves prior output and removes temporary files. All source mutations use temporary overlays; product source remains unchanged.

The /statechart reports and diagrams are in backuprestore/ and archivevalidation/. Restore distinguishes single-rename installation from per-entry installation and retains a possible partial-install failure. Eight restore steps and twenty archive steps stay within one chart label and are hidden, including terminal stuttering and file reads that retain the visible manifest state. No distinct transition class was manually hidden.

Limits: restore models two files, validation outcome, two destination modes, and install failure points. Archive models two file names and manifest presence; a successful entry read is not a fully validated backup. The models do not cover online chunk capture, archive sync/publication, all manifest fields, all JSON shapes, concurrent target modification, or power-loss behavior. The source probes do not inject every modeled install failure. The combined command also checks chunk capture, file close, archive sync/publication, and JSON nesting. See the skill and ../coverage.md for finite bounds.

## Integration update

The latest combined runner includes all extensions described in the skill. See [integration results](../integration-results.json) for its outcome. Earlier state counts above refer to the initial models. The coverage map records the final sequence scope. Product source remains unchanged.
