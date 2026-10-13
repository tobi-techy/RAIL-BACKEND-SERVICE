# Test coverage

The suite covers about 13% of the rail's statements. That is far below where a
service that is the authority for money should be, so the target for this
repository is **40%**, and it stays named in the coverage job's own output.

Gating a pull request on 40% today would mark every change red, regardless of
what it contains, and a gate that is always red stops carrying information —
people merge past it. Coverage is therefore enforced as a **ratchet**:

- `coverage-floor.txt` holds the coverage the suite actually achieves.
- `scripts/coverage-ratchet.sh coverage.out` fails when the suite measures less
  than that floor, and names the 40% target in both the passing and the failing
  message.
- The floor only rises. When coverage improves, lock the improvement in:

  ```bash
  go test -race -coverprofile=coverage.out -covermode=atomic ./...
  scripts/coverage-ratchet.sh coverage.out --update
  ```

The same shape is used for the Python agent's mypy baseline in the `miriam`
repository (`scripts/mypy_ratchet.py`): the recorded number can only improve,
and the check fails on any regression.

Lowering the floor is not a fix. If a change genuinely cannot carry a test, say
so in the pull request and let the owner decide.
