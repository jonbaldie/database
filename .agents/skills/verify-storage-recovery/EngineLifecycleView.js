export const title = "Finish or reject a storage transaction";
const labels = {active:"Transaction active",staged:"Private row staged",failed:"Commit rejected",committed:"Commit accepted",repeated:"Second commit result",closed:"Engine closed", "closed commit":"Commit after engine close", "closed begin":"Begin after engine close"};
export const state = v => `${labels[v.phase]} | ${v.finished ? "finished" : "unfinished"} | ${v.outcome}`;
export const step = a => ({Stage:"Stage a row",RejectValidation:"Schema validation fails",Commit:"Commit succeeds",RepeatCommit:"Commit again",Close:"Close the engine",CommitClosed:"Try commit after close",BeginClosed:"Try begin after close",Done:"No further work"})[a];
