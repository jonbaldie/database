# InstanceLifecycle

TLC reached 20 states and 30 steps. The chart draws 10 states and 16 arrows.
The chart does not draw 4 steps that stay in one chart state.

## ReadinessRequiresRuntime breaks
The shortest path has 4 steps:
0. Server stopped
1. You start the server: Starting server
2. Record the running state: Starting server
3. Open the runtime: Runtime started
4. Report ready: Ready for clients

## Rules that hold
NoDeadlock
