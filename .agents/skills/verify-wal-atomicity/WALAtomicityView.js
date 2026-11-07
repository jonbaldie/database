export const title = "Recovery after a table replacement";

export function state(v) {
  if (v.phase === "writing") {
    if (v.tail === "clear length") return "Clear record length written";
    if (v.tail === "clear checksum") return "Clear record checksum written";
    if (v.tail === "insert length") return "Insert record length written";
    if (v.tail === "insert checksum") return "Insert record checksum written";
    if (Object.keys(v.log).length === 0) return "Old rows saved; replacement starts";
    if (Object.keys(v.log).length === 1) return "Clear record written";
    return "Clear and insert records written";
  }
  if (v.phase === "synced") return "Replacement log synced";
  if (v.phase === "published") return "New rows visible";
  if (v.phase === "committed") return "Commit returned success";
  if (v.phase === "refused") return "Restart refuses the incomplete log";
  if (v.phase === "replaying") {
    if (v.replayIndex === 1) return "Restart reads the old rows";
    if (v.replayIndex === 2) return "Restart has cleared the rows";
    return "Restart has inserted the new row";
  }
  if (v.visible.length === 0) return "Restart exposes an empty table";
  if (v.visible.length === 1) return "Restart exposes the new row";
  return "Restart exposes the old rows";
}

export function step(action) {
  return ({
    WriteClearLength: "write the clear record length",
    WriteClearChecksum: "write the clear record checksum",
    WriteClearPayload: "write the clear record payload",
    WriteInsertLength: "write the insert record length",
    WriteInsertChecksum: "write the insert record checksum",
    WriteInsertPayload: "write the insert record payload",
    SyncWAL: "sync the log",
    PublishRows: "publish the new rows",
    ReturnSuccess: "return success",
    Crash: "the process stops",
    ReplayClear: "replay the clear record",
    ReplayInsert: "replay the insert record",
    FinishRecovery: "open the table",
    RejectTornRecord: "reject the incomplete log",
    Done: null,
  })[action];
}
