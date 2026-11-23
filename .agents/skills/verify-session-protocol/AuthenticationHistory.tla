---- MODULE AuthenticationHistory ----
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, secure, validToken, validPassword, validKeyRequest
vars == <<phase, secure, validToken, validPassword, validKeyRequest>>
Init == /\ phase="greeting" /\ secure \in BOOLEAN /\ validToken \in BOOLEAN /\ validPassword \in BOOLEAN /\ validKeyRequest \in BOOLEAN
Greeting == /\ (phase="greeting") /\ (phase'=IF secure THEN "tls request" ELSE "response") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
UpgradeTLS == /\ (phase="tls request") /\ (phase'="response") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
ReadResponse == /\ (phase="response") /\ (phase'="validate token") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
ValidateToken == /\ (phase="validate token") /\ (phase'=IF validToken THEN "request password" ELSE "rejected") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
RequestFullPassword == /\ (phase="request password") /\ (phase'=IF secure THEN "plain password" ELSE "RSA request") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
SendPublicKey == /\ (phase="RSA request") /\ (phase'=IF validKeyRequest \/ Fault="key" THEN "encrypted password" ELSE "rejected") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
ReadPassword == /\ (phase \in {"plain password","encrypted password"}) /\ (phase'="verify password") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
VerifyPassword == /\ (phase="verify password") /\ (phase'=IF validPassword \/ Fault="password" THEN "accepted" ELSE "rejected") /\ UNCHANGED <<secure,validToken,validPassword,validKeyRequest>>
Done == /\ phase \in {"accepted","rejected"} /\ UNCHANGED vars
Next == Greeting \/ UpgradeTLS \/ ReadResponse \/ ValidateToken \/ RequestFullPassword \/ SendPublicKey \/ ReadPassword \/ VerifyPassword \/ Done
AcceptedPasswordVerified == phase="accepted" => validToken /\ validPassword
RSAKeyRequiresRequest == phase="encrypted password" => validKeyRequest
====
