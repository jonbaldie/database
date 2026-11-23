export const title = "No read grant";
export const state = v => { if (v.phase === 3 && v.grants.length > 0) return "Revoked grant remains active"; return ["No read grant", "Read grant active", "Repeated grant unchanged", "Read grant revoked"][v.phase]; };
export const step = (a, from, to) => ({"Grant": "Grant read access", "GrantAgain": "Grant the same access again", "Revoke": "Revoke read access"})[a] || "Complete";
