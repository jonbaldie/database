---- MODULE OrderedCache ----
EXTENDS Naturals
CONSTANT BrokenCache
VARIABLES phase, image, cacheImage, hasCache
vars == <<phase,image,cacheImage,hasCache>>
Init == /\ phase="original" /\ image=0 /\ cacheImage=0 /\ hasCache=FALSE
BuildOriginal == /\ phase="original" /\ phase'="cached" /\ hasCache'=TRUE /\ UNCHANGED <<image,cacheImage>>
PublishRows == /\ phase="cached" /\ phase'="published" /\ image'=1 /\ hasCache'=BrokenCache /\ UNCHANGED cacheImage
ReadIndex == /\ phase="published" /\ phase'="read" /\ cacheImage'=(IF hasCache THEN cacheImage ELSE image) /\ hasCache'=TRUE /\ UNCHANGED image
Done == /\ phase="read" /\ UNCHANGED vars
Next == BuildOriginal \/ PublishRows \/ ReadIndex \/ Done
IndexMatchesRows == phase="read" => cacheImage=image
====
