# AuthenticationHistory

TLC reached 100 states and 100 steps. The chart draws 12 states and 15 arrows.
The chart does not draw 16 steps that stay in one chart state.

## RSAKeyRequiresRequest breaks
The shortest path has 5 steps:
0. greeting
1. Server sends greeting: response
2. Client sends authentication response: validate token
3. Initial token is valid: request password
4. Server requests full password: RSA request
5. Server sends RSA key without a valid request: RSA key sent without request

## Rules that hold
AcceptedPasswordVerified, NoDeadlock
