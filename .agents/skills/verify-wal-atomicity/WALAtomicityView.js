export const title = "Recovery at transaction boundaries";

export function state(v) {
  if (v.phase === "writing") return "Writing a transaction";
  if (v.phase === "synced") return "Transaction log synced";
  if (v.phase === "published") return "Committed rows visible";
  if (v.phase === "committed") return "Commit returned success";
  if (v.phase === "refused") return "Restart refuses a torn record";
  if (v.phase === "replaying") {
    return v.pending === "none"
      ? "Restart reads committed or legacy rows"
      : "Restart buffers an open transaction";
  }
  return "Restart exposes only committed and legacy rows";
}

export function step(action) {
  return ({
    WriteLength: "write a record length",
    WriteChecksum: "write a record checksum",
    WritePayload: "write a record payload",
    SyncWAL: "sync the log",
    PublishRows: "publish the rows",
    ReturnSuccess: "return success",
    Crash: "the process stops",
    ReplayBegin: "begin a transaction and discard any abandoned work",
    ReplayOperation: "read an operation",
    ReplayCommit: "apply the complete transaction",
    FinishRecovery: "open and discard any unfinished transaction",
    RejectTornRecord: "reject the incomplete record",
    AppendLaterTransaction: "append a later transaction after restart",
    Done: null,
  })[action];
}
