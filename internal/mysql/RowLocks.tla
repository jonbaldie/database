---- MODULE RowLocks ----
\* One step is one critical section of the row lock manager.
\*
\* AcquireRows and RetryWait are lockManager.waitAttempt.
\*   internal/mysql/locking.go
\*     conflictingOwners, waitGraphReaches, waitAttempt, grant
\* NowaitRows is lockNowait and lockManager.skipLocked.
\* ReleaseSession is lockManager.release, called from
\*   statementTransaction.abort, rollbackTransaction, and
\*   session.finishTransaction in internal/mysql/transaction.go.
\*
\* release deletes only the releasing session's outgoing wait edges.
\* A later waitAttempt reads the edges that are still stored.

EXTENDS FiniteSets

CONSTANTS Sessions, Rows

VARIABLES grant, waitFor, phase, wantRows, wantMode, justified

vars == <<grant, waitFor, phase, wantRows, wantMode, justified>>

Modes == {"none", "S", "X"}

Conflict(held, wanted) ==
  held # "none" /\ wanted # "none" /\ (held = "X" \/ wanted = "X")

Upgrade(held, mode) ==
  IF mode = "X" \/ held = "none" THEN mode ELSE held

Owners(s, rows, mode) ==
  {o \in Sessions :
    o # s /\ \E r \in rows : Conflict(grant[o][r], mode)}

GrantTo(s, rows, mode) ==
  [grant EXCEPT ![s] =
    [r \in Rows |-> IF r \in rows THEN Upgrade(grant[s][r], mode) ELSE grant[s][r]]]

\* Paths of at most three edges. The model has three sessions and one row,
\* so a simple path is no longer than this.
StoredHits(seeds, target) ==
  \/ target \in seeds
  \/ \E a \in seeds : target \in waitFor[a]
  \/ \E a \in seeds : \E b \in waitFor[a] : target \in waitFor[b]
  \/ \E a \in seeds : \E b \in waitFor[a] : \E c \in waitFor[b] : target \in waitFor[c]

RealOwners(actor, actorOwners, sess) ==
  IF sess = actor THEN actorOwners
  ELSE IF phase[sess] = "waiting"
       THEN Owners(sess, wantRows[sess], wantMode[sess])
       ELSE {}

\* A cycle through actor on the graph of locks that are held now.
RealCycle(actor, actorOwners) ==
  LET edge == [sess \in Sessions |-> RealOwners(actor, actorOwners, sess)]
  IN \/ actor \in edge[actor]
     \/ \E a \in edge[actor] : actor \in edge[a]
     \/ \E a \in edge[actor] : \E b \in edge[a] : actor \in edge[b]

Decide(s, rows, mode, fromPhase) ==
  /\ phase[s] = fromPhase
  /\ rows # {}
  /\ LET owners == Owners(s, rows, mode)
     IN \/ /\ owners = {}
           /\ grant' = GrantTo(s, rows, mode)
           /\ waitFor' = [waitFor EXCEPT ![s] = {}]
           /\ phase' = [phase EXCEPT ![s] = "idle"]
           /\ justified' = [justified EXCEPT ![s] = TRUE]
           /\ UNCHANGED <<wantRows, wantMode>>
        \/ /\ owners # {}
           /\ StoredHits(owners, s)
           /\ waitFor' = [waitFor EXCEPT ![s] = {}]
           /\ phase' = [phase EXCEPT ![s] = "deadlock"]
           /\ wantRows' = [wantRows EXCEPT ![s] = rows]
           /\ wantMode' = [wantMode EXCEPT ![s] = mode]
           /\ justified' = [justified EXCEPT ![s] = RealCycle(s, owners)]
           /\ UNCHANGED grant
        \/ /\ owners # {}
           /\ ~StoredHits(owners, s)
           /\ waitFor' = [waitFor EXCEPT ![s] = owners]
           /\ phase' = [phase EXCEPT ![s] = "waiting"]
           /\ wantRows' = [wantRows EXCEPT ![s] = rows]
           /\ wantMode' = [wantMode EXCEPT ![s] = mode]
           /\ justified' = [justified EXCEPT ![s] = TRUE]
           /\ UNCHANGED grant

AcquireRows ==
  \E s \in Sessions : \E rows \in SUBSET Rows : \E mode \in {"S", "X"} :
    Decide(s, rows, mode, "idle")

RetryWait ==
  \E s \in Sessions : Decide(s, wantRows[s], wantMode[s], "waiting")

NowaitRows ==
  \E s \in Sessions : \E rows \in SUBSET Rows : \E mode \in {"S", "X"} :
    /\ phase[s] = "idle"
    /\ rows # {}
    /\ Owners(s, rows, mode) = {}
    /\ grant' = GrantTo(s, rows, mode)
    /\ waitFor' = [waitFor EXCEPT ![s] = {}]
    /\ justified' = [justified EXCEPT ![s] = TRUE]
    /\ UNCHANGED <<phase, wantRows, wantMode>>

ReleaseSession ==
  \E s \in Sessions :
    /\ \/ phase[s] \in {"waiting", "deadlock"}
       \/ \E r \in Rows : grant[s][r] # "none"
    /\ grant' = [grant EXCEPT ![s] = [r \in Rows |-> "none"]]
    /\ waitFor' = [waitFor EXCEPT ![s] = {}]
    /\ phase' = [phase EXCEPT ![s] = "idle"]
    /\ wantRows' = [wantRows EXCEPT ![s] = {}]
    /\ justified' = [justified EXCEPT ![s] = TRUE]
    /\ UNCHANGED wantMode

Init ==
  /\ grant = [s \in Sessions |-> [r \in Rows |-> "none"]]
  /\ waitFor = [s \in Sessions |-> {}]
  /\ phase = [s \in Sessions |-> "idle"]
  /\ wantRows = [s \in Sessions |-> {}]
  /\ wantMode = [s \in Sessions |-> "S"]
  /\ justified = [s \in Sessions |-> TRUE]

Next == AcquireRows \/ RetryWait \/ NowaitRows \/ ReleaseSession

Spec == Init /\ [][Next]_vars

\* Two sessions never hold conflicting locks on one row.
MutualExclusion ==
  \A r \in Rows : \A s1, s2 \in Sessions :
    s1 # s2 => ~Conflict(grant[s1][r], grant[s2][r])

\* A deadlock report is a cycle in the locks that sessions hold now,
\* not a cycle that uses a wait edge left behind by release.
JustifiedDeadlock ==
  \A s \in Sessions : phase[s] = "deadlock" => justified[s]

LiveEdge(s) ==
  IF phase[s] = "waiting" THEN Owners(s, wantRows[s], wantMode[s]) ELSE {}

\* Sessions that wait for one another's current locks are not left waiting.
NoLiveCycle ==
  \A s \in Sessions :
    phase[s] = "waiting" =>
      LET edge == [t \in Sessions |-> LiveEdge(t)]
      IN ~(\/ s \in edge[s]
           \/ \E a \in edge[s] : s \in edge[a]
           \/ \E a \in edge[s] : \E b \in edge[a] : s \in edge[b])

====
