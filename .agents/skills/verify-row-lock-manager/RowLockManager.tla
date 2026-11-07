---- MODULE RowLockManager ----
EXTENDS FiniteSets, TLC

CONSTANTS Sessions, Resources, Unlocked, Shared, Exclusive

Modes == {Shared, Exclusive}
LockValues == Modes \cup {Unlocked}
ResourceRequests == SUBSET Resources \ {{}}

VARIABLES granted, held, waiting, outcome
vars == <<granted, held, waiting, outcome>>

RECURSIVE Reaches(_, _, _)
Reaches(frontier, target, visited) ==
    \E owner \in frontier:
        owner = target
        \/ (owner \notin visited
            /\ Reaches(waiting[owner], target, visited \cup {owner}))

ConflictingOwners(session, resources, wanted) ==
    {owner \in Sessions \ {session}:
        \E resource \in resources:
            granted[resource][owner] = Exclusive
            \/ (wanted = Exclusive /\ granted[resource][owner] = Shared)}

Init ==
    /\ granted = [resource \in Resources |-> [owner \in Sessions |-> Unlocked]]
    /\ held = [owner \in Sessions |-> [resource \in Resources |-> Unlocked]]
    /\ waiting = [owner \in Sessions |-> {}]
    /\ outcome = [owner \in Sessions |-> "none"]

GrantRequest(session, resources, mode) ==
    LET owners == ConflictingOwners(session, resources, mode)
    IN
        /\ owners = {}
        /\ granted' =
            [resource \in Resources |->
                [owner \in Sessions |->
                    IF owner = session /\ resource \in resources
                    THEN IF mode = Exclusive \/ granted[resource][owner] = Unlocked
                         THEN mode ELSE granted[resource][owner]
                    ELSE granted[resource][owner]]]
        /\ held' =
            [owner \in Sessions |->
                [resource \in Resources |->
                    IF owner = session /\ resource \in resources
                    THEN IF mode = Exclusive \/ held[owner][resource] = Unlocked
                         THEN mode ELSE held[owner][resource]
                    ELSE held[owner][resource]]]
        /\ waiting' = [waiting EXCEPT ![session] = {}]
        /\ outcome' = [outcome EXCEPT ![session] = "granted"]

WaitRequest(session, resources, mode) ==
    LET owners == ConflictingOwners(session, resources, mode)
    IN
        /\ owners # {}
        /\ ~Reaches(owners, session, {})
        /\ waiting' = [waiting EXCEPT ![session] = owners]
        /\ outcome' = [outcome EXCEPT ![session] = "waiting"]
        /\ UNCHANGED <<granted, held>>

RejectDeadlock(session, resources, mode) ==
    LET owners == ConflictingOwners(session, resources, mode)
    IN
        /\ owners # {}
        /\ Reaches(owners, session, {})
        /\ waiting' = [waiting EXCEPT ![session] = {}]
        /\ outcome' = [outcome EXCEPT ![session] = "deadlock"]
        /\ UNCHANGED <<granted, held>>

Release(session) ==
    /\ granted' =
        [resource \in Resources |->
            [owner \in Sessions |->
                IF owner = session THEN Unlocked ELSE granted[resource][owner]]]
    /\ held' =
        [owner \in Sessions |->
            IF owner = session
            THEN [resource \in Resources |-> Unlocked]
            ELSE held[owner]]
    /\ waiting' = [waiting EXCEPT ![session] = {}]
    /\ outcome' = [outcome EXCEPT ![session] = "released"]

Next ==
    \/ \E session \in Sessions:
         \E resources \in ResourceRequests:
          \E mode \in Modes: GrantRequest(session, resources, mode)
    \/ \E session \in Sessions:
         \E resources \in ResourceRequests:
          \E mode \in Modes: WaitRequest(session, resources, mode)
    \/ \E session \in Sessions:
         \E resources \in ResourceRequests:
          \E mode \in Modes: RejectDeadlock(session, resources, mode)
    \/ \E session \in Sessions: Release(session)

Spec == Init /\ [][Next]_vars

NoConflictingGrants ==
    \A resource \in Resources:
        \A first, second \in Sessions:
            first # second
            /\ granted[resource][first] # Unlocked
            /\ granted[resource][second] # Unlocked
            => granted[resource][first] = Shared
               /\ granted[resource][second] = Shared

LockIndexesMatch ==
    \A owner \in Sessions:
        \A resource \in Resources:
            held[owner][resource] = granted[resource][owner]

WaitGraphAcyclic ==
    \A owner \in Sessions: ~Reaches(waiting[owner], owner, {})

DeadlockResponseClearsWait ==
    \A owner \in Sessions:
        outcome[owner] = "deadlock" => waiting[owner] = {}

====
