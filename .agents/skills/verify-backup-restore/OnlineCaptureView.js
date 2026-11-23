export const title = "Capture online backup files";
export const state = v => v.phase;
export const step = a => ({ReadChunk:"Append a file chunk",FinishRows:"Finish reading rows",SyncFile:"Sync a captured file",CloseFile:"Close a captured file",Advance:"Proceed to next required file",MissingFile:"Required file is missing",Complete:"Both files are closed",Done:"Complete"})[a];
