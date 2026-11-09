export const title = "Upgrade a stopped instance or resume an interrupted upgrade";
export const state = v => v.phase;
export const step = a => ({WriteMarker:"Record upgrade target",MarkIncomplete:"Record incomplete upgrade",WriteTarget:"Install target version",RemoveMarker:"Remove upgrade marker",Interrupt:"Upgrade stops",RequestDifferentTarget:"You request a different target",RequestSameTarget:"You request the same target",Resume:"Resume the upgrade",Done:"Complete"})[a];
