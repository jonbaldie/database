export const title = "Shutdown request ownership";
export const state = s => s.requested ? `Shutdown requested by ${s.operation}` : "Server accepts requests";
export const step = (a,from,to) => to.requests > from.requests ? "Request shutdown" : "Wait";
