import os
import subprocess
import sys


def lines(command):
    result = subprocess.run(command, shell=True, capture_output=True, text=True)
    output = result.stdout + result.stderr
    if result.returncode != 0:
        sys.stderr.write(output)
        sys.exit(result.returncode)
    return [line for line in output.splitlines() if line]


def finding_lines(command):
    result = subprocess.run(command, shell=True, capture_output=True, text=True)
    output = result.stdout + result.stderr
    if result.returncode not in (0, 1) or (result.returncode != 0 and not output):
        sys.stderr.write(output)
        sys.exit(result.returncode)
    return [line for line in output.splitlines() if line]


def main():
    files = lines(
        "find . -name '*.go'"
        " -not -path './vendor/*'"
        " -not -path './Godeps/*'"
        " -not -path './third_party/*'"
        " -not -path './testdata/*'"
        " -not -name '*.pb.go'"
        " -not -name '*.pb.gw.go'"
        " -not -name '*.generated.go'"
        " -not -name 'bindata.go'"
        " -not -name '*_string.go'"
    )
    file_count = len(files)
    if file_count == 0:
        raise RuntimeError("no Go files to score")
    file_arguments = " ".join(files)

    gofmt_failures = len(lines(f"gofmt -s -l {file_arguments}"))
    gofmt = (file_count - gofmt_failures) / file_count

    vet_result = subprocess.run("go vet ./...", shell=True, capture_output=True, text=True)
    if vet_result.returncode == 0:
        vet = 1.0
    else:
        vet_failures = set()
        for line in (vet_result.stdout + vet_result.stderr).splitlines():
            if not line or line.startswith("#"):
                continue
            filename = line.split(":")[0].lstrip("./")
            if filename.endswith(".go"):
                vet_failures.add(filename)
        if not vet_failures:
            sys.stderr.write(vet_result.stdout + vet_result.stderr)
            raise RuntimeError("go vet failed without a Go-file diagnostic")
        vet = (file_count - len(vet_failures)) / file_count

    gocyclo = os.environ["GOREPORTCARD_GOCYCLO"]
    cyclo_failures = {line.split()[-1].split(":")[0] for line in finding_lines(f"{gocyclo} -over 15 .")}
    cyclo = (file_count - len(cyclo_failures)) / file_count

    prefixes = {"license", "copying", "copyright", "licence", "unlicense", "copyleft"}
    license_score = float(any(any(name.lower().startswith(prefix) for prefix in prefixes) for name in os.listdir(".")))

    ineffassign = 1.0
    if file_count <= 100:
        ineffassign_command = os.environ["GOREPORTCARD_INEFFASSIGN"]
        ineffassign_failures = set()
        for line in finding_lines(f"{ineffassign_command} ./..."):
            if ":" not in line:
                continue
            filename = line.split(":")[0].lstrip("./")
            if filename in files or "./" + filename in files:
                ineffassign_failures.add(filename)
        ineffassign = (file_count - len(ineffassign_failures)) / file_count

    checks = [
        ("gofmt", gofmt, 0.30),
        ("go vet", vet, 0.30),
        ("gocyclo", cyclo, 0.10),
        ("license", license_score, 0.05),
        ("ineffassign", ineffassign, 0.10),
    ]
    total_weight = sum(weight for _, _, weight in checks)
    score = sum(rate * weight for _, rate, weight in checks) / total_weight

    for name, rate, weight in checks:
        print(f"  {name:<14} {rate:6.1%}  weight={weight:.2f}")
    print(f"  {'score':<14} {score:6.1%}")
    grade = "A+" if score > 0.90 else "A" if score > 0.80 else "B" if score > 0.70 else "<B"
    print(f"  {'grade':<14} {grade}")
    if score <= 0.90:
        print("FAIL: score must be > 90% for A+")
        sys.exit(1)
    print("PASS: A+")


if __name__ == "__main__":
    main()
