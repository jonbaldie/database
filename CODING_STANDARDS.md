# Coding Standards

- Production Go should report no `messgo` violations on the codesize,design rulesets.
- Production Go should report a `mutago` covered-MSI of 80% or higher.

## Common Footguns to Avoid

- Marking parent GitHub issue as resolved when not all its child issues are resolved.
- Writing exclusively unit tests without integration or end-to-end tests.
- Neglecting to exercise real system behaviour: 'The tests pass so it must work.'
