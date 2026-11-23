# Shutdown polling results

At source baseline fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1, ShutdownPolling passed over 25 states. Two broken configs failed ThreeConsecutiveFailures. Two matching source overlays failed their behavioral assertions. The current probe passed. The combined instance runner passed.

The model allows at most six attempts. The Go overlay changes only clock and connection dependencies in waitForOnlineShutdown. Two failed connections, one success, then three failures require six attempts. Repeated successes reach the deadline. Removing the reset or changing the threshold is caught.

This tests polling with a fake clock and controlled connection outcomes. It does not test live runtime drain, real network timing, or process exit. The checked chart is in shutdownpolling/chart.mmd. It merges equal failure-count labels and omits same-label steps. The report records 25 states, 40 steps, five labels, eight arrows, and 13 same-label steps.
