export const title = 'Reject an autocommit mutation';
export const state = v => v.phase === "statement failed" && v.staged ? "Failed statement retained changes" : v.phase;
export const step = (a,f,t) => ({Fail:"Reject autocommit statement",Done:"Complete"})[a];
