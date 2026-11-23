export const title = "Allow one replacement for one catalog revision";
export const state = v => `Requests checked ${v.processed}, replacements accepted ${v.successes}`;
export const step = (a,from,to) => a === "ApplyRequest" ? (to.successes > from.successes ? "Accept first revision claim" : "Reject revision already claimed") : "Complete";
