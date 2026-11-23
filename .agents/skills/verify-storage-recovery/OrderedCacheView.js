export const title = "Rebuild an index when the row image changes";
export const state = v => v.phase === "read" && v.cacheImage !== v.image ? "Stale index used" : ({original:"Original rows",cached:"Original index cached",published:"New row image published",read:"Index matches new rows"})[v.phase];
export const step = a => ({BuildOriginal:"Build original index",PublishRows:"Publish different rows",ReadIndex:"Build index for new rows",Done:"Complete"})[a];
