# Prototype: v0.1 product boundary

> THROWAWAY DECISION PROTOTYPE — this artifact records the boundary considered while specifying v0.1. It is not product documentation or implementation guidance.

## Question

Which interfaces belong to the v0.1 database server product, and does the repository expose a supported public Go API?

## Proposed boundary

The product is a standalone database server. Applications and operators interact with the server through documented external contracts:

1. The MySQL application compatibility profile, including its network protocol, authentication, session, prepared-statement, result, metadata, and failure behaviour.
2. The supported SQL surface for relational data, transactions, schema and account management, query explanation, introspection, and operational control.
3. The stable tabular query-explanation projection and canonical, versioned JSON query-explanation format.
4. The server's documented startup, configuration, shutdown, diagnostics, validation, backup, and restore behaviour.
5. Release-scoped compatibility, limits, supported platforms, and resource guarantees.

The repository's Go module is the implementation source and build identity of that server. v0.1 does not promise:

- an importable Go library API;
- embedded database operation;
- stable Go packages or component interfaces;
- extension or plugin APIs;
- stable internal file formats, algorithms, or process boundaries.

Internal decomposition may evolve freely as long as the documented server contracts remain true.

## Boundary sketch

```text
application or operator
        |
        | documented MySQL, SQL, explanation, and operational contracts
        v
  v0.1 database server
        |
        | no public compatibility promise
        v
  Go packages and internal components
```

## Verdict

Accepted: v0.1 is only a database server product. The Go module does not add a second, library-shaped product surface.
