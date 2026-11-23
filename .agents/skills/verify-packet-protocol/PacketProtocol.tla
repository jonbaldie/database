--------------------------- MODULE PacketProtocol ---------------------------
EXTENDS Naturals
CONSTANT Fixed
VARIABLES phase, first, expected
vars == <<phase, first, expected>>
Init == /\ phase = "first"
        /\ first \in {0, 254, 255}
        /\ expected = 0
ReadFirstHeader == /\ phase = "first"
                   /\ phase' = "continuation"
                   /\ expected' = (first + IF Fixed THEN 1 ELSE 2) % 256
                   /\ UNCHANGED first
ReadValidContinuation == /\ phase = "continuation"
                         /\ phase' = IF expected = (first + 1) % 256 THEN "accepted" ELSE "rejected"
                         /\ UNCHANGED <<first, expected>>
Done == /\ phase \in {"accepted", "rejected"}
        /\ UNCHANGED vars
Next == ReadFirstHeader \/ ReadValidContinuation \/ Done
ValidMessageAccepted == phase # "rejected"
=============================================================================
