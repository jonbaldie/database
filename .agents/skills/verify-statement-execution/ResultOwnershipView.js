export const title = 'Close a result on success or failure';
export const state = v => v.calls > 1 ? "Result released twice" : v.phase;
export const step = (a,f,t) => a === "Deliver" ? (t.phase === "delivery failed" ? "Row delivery fails" : "Finish row delivery") : ({Close:"Release owned resources",Done:"Complete"})[a];
