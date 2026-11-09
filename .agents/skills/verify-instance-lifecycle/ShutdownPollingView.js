export const title = "Confirm that the database listener stopped";
export const state = v => v.phase === "polling" ? `Wait for stop, consecutive failures ${v.streak}` : v.phase === "stopped" ? "Listener stop confirmed" : "Shutdown wait timed out";
export const step = (a,from,to) => ({DialFails:"Connection attempt fails",DialSucceeds:"Listener accepts connection; reset failure count",Deadline:"Shutdown deadline reached",Done:"Complete"})[a];
