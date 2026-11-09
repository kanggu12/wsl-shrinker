#!/bin/sh
# Run every fuzz target for FUZZTIME (the first argument, like 30s), one after
# another. A failing input lands in testdata/fuzz and fails the run.
#
# Go's fuzzing engine now and then ends a run at its time limit with "context
# deadline exceeded" although it found nothing: no failing input is written,
# and the same run passes when tried again. Such a run is tried once more. Any
# other failure, or the same one twice, stops.
set -u
fuzztime=${1:-30s}
for pkg in ./internal/machine/vhdx ./internal/machine/ext4; do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz'); do
    for try in 1 2; do
      out=$(go test -run '^$' -fuzz "^$target\$" -fuzztime "$fuzztime" "$pkg" 2>&1)
      status=$?
      printf '%s\n' "$out"
      [ "$status" = 0 ] && break
      # The engine's own deadline, with nothing found: once more.
      if [ "$try" = 1 ] && printf '%s\n' "$out" | grep -q 'context deadline exceeded' &&
        ! printf '%s\n' "$out" | grep -q 'Failing input written'; then
        echo "fuzz: $target stopped on the engine's deadline with nothing found; once more"
        continue
      fi
      exit 1
    done
  done
done
