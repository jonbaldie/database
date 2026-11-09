export const title = "Restore a backup into a new or empty directory";
export const state = v => v.phase;
export const step = (a, from, to) => a === "Validate" ? (to.phase === "rejected" ? "Staged validation fails" : "Staged validation succeeds") : ({Extract:"Extract into staging",Validate:"Validate staged data",InstallNewDirectory:"Install the new directory",InstallFirstEntry:"Move first entry into existing empty directory",InstallSecondEntry:"Move second entry",RemoveStaging:"Remove staging directory",InstallFails:"Installation fails",Done:"Complete"})[a];
