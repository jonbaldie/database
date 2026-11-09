# AuthenticationHistory

TLC reached 96 states and 96 steps. The chart draws 12 states and 14 arrows.
The chart does not draw 16 steps that stay in one chart state.

## AcceptedPasswordVerified breaks
The shortest path has 7 steps:
0. greeting
1. Server sends greeting: response
2. Client sends authentication response: validate token
3. Initial token is valid: request password
4. Server requests full password: RSA request
5. Client requests RSA key; server sends it: encrypted password
6. Client sends full password: verify password
7. Full password is accepted: Accepted invalid credentials

## Rules that hold
RSAKeyRequiresRequest, NoDeadlock
