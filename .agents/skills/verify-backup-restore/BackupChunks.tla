-------------------------- MODULE BackupChunks --------------------------
EXTENDS Naturals, Sequences
CONSTANT BrokenClear, BrokenTail
VARIABLES phase, buffered, emitted, consumed
vars == <<phase, buffered, emitted, consumed>>
Input == <<"a", "b", "c">>
Init == /\ phase = "writing" /\ buffered = <<>> /\ emitted = <<>> /\ consumed = 0
BufferByte == /\ phase = "writing" /\ consumed < 3 /\ Len(buffered) < 2
              /\ buffered' = Append(buffered, Input[consumed+1]) /\ consumed' = consumed+1
              /\ UNCHANGED <<phase, emitted>>
FlushFull == /\ phase = "writing" /\ Len(buffered) = 2 /\ Len(emitted) < 4
             /\ emitted' = emitted \o buffered /\ buffered' = IF BrokenClear THEN buffered ELSE <<>>
             /\ UNCHANGED <<phase, consumed>>
FlushTail == /\ phase = "writing" /\ consumed = 3 /\ Len(buffered) < 2
             /\ emitted' = (IF BrokenTail THEN emitted ELSE emitted \o buffered) /\ buffered' = <<>> /\ phase' = "done"
             /\ UNCHANGED consumed
Done == /\ phase = "done" /\ UNCHANGED vars
FaultSaturated == /\ BrokenClear /\ Len(emitted) >= 4 /\ UNCHANGED vars
Next == BufferByte \/ FlushFull \/ FlushTail \/ Done \/ FaultSaturated
BufferedAndEmittedMatchInput == emitted \o buffered = SubSeq(Input,1,consumed)
CompletedStreamMatchesInput == phase = "done" => emitted = Input
=============================================================================
