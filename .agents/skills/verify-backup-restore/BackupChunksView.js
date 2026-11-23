export const title = "Stream catalog chunks";
export const state = v => `${v.phase} | consumed ${v.consumed} | buffer ${Object.keys(v.buffered).length} | emitted ${Object.keys(v.emitted).length}`;
export const step = a => a;
