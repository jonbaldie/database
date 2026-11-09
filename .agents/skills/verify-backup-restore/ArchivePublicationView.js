export const title = "Write and publish a backup archive";
export const state = v => v.phase;
export const step = a => ({Write:"Write archive contents",Sync:"Sync archive file",Close:"Close archive file",Rename:"Install archive at output path",Fail:"Operation fails",Done:"Complete"})[a];
