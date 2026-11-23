export const title = "Send a message across two frames";
export const state = v => ({first: "Reading first frame", continuation: "Waiting for next frame", accepted: "Message accepted", rejected: "Valid message rejected"})[v.phase];
export const step = a => ({ReadFirstHeader: "First frame arrives", ReadValidContinuation: "Next consecutive frame arrives", Done: "Complete"})[a];
