# Stop consolidation fixtures

Test fixtures for `internal/consolidation`, which reads the stop
consolidation files Java OneBusAway uses. Each `*.expected.tsv` file was
produced by Java OneBusAway's own parser, not written by hand. That is
what makes these parity tests.

- `puget_sound.txt`: the historical Puget Sound consolidation file, which
  used to be a Trac wiki page. Copied from
  `onebusaway/obacloud` (`spec/fixtures/files/puget_sound_stop_consolidation.md`).
- `java_parity/*.txt`: small files, one per parsing edge case.
- `*.expected.tsv`: the replacements Java built from the matching file,
  one `[absorbed agency|id]<TAB>[canonical agency|id]` line per entry,
  sorted. `ERROR<TAB>...` means Java rejected the file.

## Regenerating the expected output

`JavaParity.java` calls `EntityReplacementStrategyFactory` from
onebusaway-application-modules. Build that module, then run:

```sh
JAR=../app-modules/onebusaway-transit-data-federation-builder/target/onebusaway-transit-data-federation-builder-2.7.2-withAllDependencies.jar
javac -proc:none -cp "$JAR" -d /tmp/java-parity testdata/stop_consolidation/JavaParity.java
for f in testdata/stop_consolidation/puget_sound.txt testdata/stop_consolidation/java_parity/*.txt; do
  java -cp "$JAR:/tmp/java-parity" JavaParity "$f" > "${f%.txt}.expected.tsv"
done
```
