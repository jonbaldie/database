export const title = "Connect and execute one statement";
export const state = v => v.phase;
export const step = action => ({Greeting:"Server sends greeting",AuthenticationFails:"Authentication fails",AuthenticationSucceeds:"Password exchange succeeds",CapacityFull:"Session capacity is full",Admit:"Session slot available",Start:"Client submits a supported text statement",InstallWatch:"Install cancellation watch",ShowActive:"Publish active statement",Cancel:"Client requests cancellation",Finish:"Statement returns",Disconnect:"Client disconnects",Done:"Complete"})[action];
