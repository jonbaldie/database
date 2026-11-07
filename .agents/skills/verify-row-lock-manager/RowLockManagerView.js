export const title = "Two sessions request locks on one database row";

export const state = (v) => {
  const outcomes = Object.values(v.outcome);
  if (outcomes.includes("deadlock")) return "Deadlock request rejected";
  if (outcomes.includes("waiting")) return "A session waits for a lock owner";
  if (Object.values(v.granted).some((owners) =>
    Object.values(owners).some((mode) => mode !== "unlocked"))) {
    return "Compatible row locks held";
  }
  return "No row locks held";
};

export const step = (action) => {
  const name = String(action);
  if (name.includes("RejectDeadlock")) return "the manager rejects a deadlock request";
  if (name.includes("WaitRequest")) return "a session waits for the lock owner";
  if (name.includes("GrantRequest")) return "a session acquires a compatible lock";
  if (name.includes("Release")) return "a session releases its locks";
  return null;
};
