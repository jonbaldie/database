# InstanceLifecycle

TLC reached 20 states and 30 steps. The chart draws 10 states and 16 arrows.
The chart does not draw 4 steps that stay in one chart state.

## CleanStopHasStoppedMarker breaks
The shortest path has 8 steps:
0. Server stopped
1. You start the server: Starting server
2. Record the running state: Starting server
3. Open the runtime: Runtime started
4. Report ready: Ready for clients
5. You request shutdown: Stopping new work
6. Finish shutdown: Runtime closed
7. Record a clean stop: Clean stop recorded
8. Release the data directory: Stopped cleanly

## Rules that hold
NoDeadlock
