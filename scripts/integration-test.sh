#!/bin/bash

# MOCK INTEGRATION TESTS

echo "Starting integration tests..."

sleep 2

echo "% bin/busted -o gtest spec/03-plugins/01-tcp-log/
2026/06/11 00:24:42 [warn] 68047#0: LMDB database is corrupted or incompatible, removing
[==========] Running tests from scanned files.
[----------] Global test environment setup.
[----------] Running tests from spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:36: tcp-log : (schema) empty config, global tls_certificate_verify disabled
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:36: tcp-log : (schema) empty config, global tls_certificate_verify disabled (2.37 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:50: tcp-log : (schema) config.ssl_verify = true when global option disabled
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:50: tcp-log : (schema) config.ssl_verify = true when global option disabled (2.42 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:68: tcp-log : (schema) config.ssl_verify = false when global option enabled
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:68: tcp-log : (schema) config.ssl_verify = false when global option enabled (0.83 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:108: tcp-log : (schema), tls_certificate_verify = true empty config
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:108: tcp-log : (schema), tls_certificate_verify = true empty config (0.57 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:115: tcp-log : (schema), tls_certificate_verify = true config.ssl_verify = falsewhen global option enabled
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:115: tcp-log : (schema), tls_certificate_verify = true config.ssl_verify = falsewhen global option enabled (2.01 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:341: Plugin: tcp-log (log) [#postgres] #flaky logs to TCP
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:341: Plugin: tcp-log (log) [#postgres] #flaky logs to TCP (12.32 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:368: Plugin: tcp-log (log) [#postgres] #flaky custom log values by lua logs custom values
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:368: Plugin: tcp-log (log) [#postgres] #flaky custom log values by lua logs custom values (5.64 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:397: Plugin: tcp-log (log) [#postgres] #flaky custom log values by lua unsets existing log values
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:397: Plugin: tcp-log (log) [#postgres] #flaky custom log values by lua unsets existing log values (5.18 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:424: Plugin: tcp-log (log) [#postgres] #flaky logs to TCP (#grpc)
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:424: Plugin: tcp-log (log) [#postgres] #flaky logs to TCP (#grpc) (131.30 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:454: Plugin: tcp-log (log) [#postgres] #flaky logs proper latencies
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:454: Plugin: tcp-log (log) [#postgres] #flaky logs proper latencies (1009.51 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:487: Plugin: tcp-log (log) [#postgres] #flaky logs proper latencies (#grpc)
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:487: Plugin: tcp-log (log) [#postgres] #flaky logs proper latencies (#grpc) (69.83 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:524: Plugin: tcp-log (log) [#postgres] #flaky logs proper latencies (#grpcs)
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:524: Plugin: tcp-log (log) [#postgres] #flaky logs proper latencies (#grpcs) (85.02 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:562: Plugin: tcp-log (log) [#postgres] #flaky performs a TLS handshake on the remote TCP server
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:562: Plugin: tcp-log (log) [#postgres] #flaky performs a TLS handshake on the remote TCP server (7.71 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:585: Plugin: tcp-log (log) [#postgres] #flaky logs TLS info
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:585: Plugin: tcp-log (log) [#postgres] #flaky logs TLS info (7.16 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:611: Plugin: tcp-log (log) [#postgres] #flaky TLS client_verify can be overwritten
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:611: Plugin: tcp-log (log) [#postgres] #flaky TLS client_verify can be overwritten (7.58 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:638: Plugin: tcp-log (log) [#postgres] #flaky logs TLS info (#grpcs)
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:638: Plugin: tcp-log (log) [#postgres] #flaky logs TLS info (#grpcs) (56.67 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:668: Plugin: tcp-log (log) [#postgres] #flaky tries field encoded as JSON array instead of object #6390
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:668: Plugin: tcp-log (log) [#postgres] #flaky tries field encoded as JSON array instead of object #6390 (4.52 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:689: Plugin: tcp-log (log) [#postgres] #flaky populates tries field when proxying to an upstream service
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:689: Plugin: tcp-log (log) [#postgres] #flaky populates tries field when proxying to an upstream service (5.35 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:717: Plugin: tcp-log (log) [#postgres] #flaky #stream reports tcp streams
[       OK ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:717: Plugin: tcp-log (log) [#postgres] #flaky #stream reports tcp streams (5.35 ms)
[ RUN      ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:753: Plugin: tcp-log (log) [#postgres] #flaky #stream reports tls streams
"

sleep 2

echo "
2026/06/11 00:24:54 [warn] 68047#0: *2 [lua] globalpatches.lua:646: sslhandshake(): detected attempt to disable certificate verification while global tls_certificate_verify option is enabled., context: ngx.timer
spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:760: detected attempt to disable certificate verification while global tls_certificate_verifyoption is enabled.
[  FAILED  ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:753: Plugin: tcp-log (log) [#postgres] #flaky #stream reports tls streams (4.05 ms)
Error from thread: ./spec/internal/server.lua:69: timeout
stack traceback:
        [C]: in function 'assert'
        ./spec/internal/server.lua:69: in function <./spec/internal/server.lua:45>
./spec/internal/db.lua:388: [PostgreSQL error] failed to retrieve PostgreSQL server_version_num:
[----------] 20 tests from spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua (71970.42 ms total)
" >&2

sleep 2

echo "
[----------] Global test environment teardown.
[==========] 20 tests from 1 test file ran. (71971.58 ms total)
[  PASSED  ] 19 tests.
[  FAILED  ] 1 test, listed below:
[  FAILED  ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:753: Plugin: tcp-log (log) [#postgres] #flaky #stream reports tls streams
[  ERROR   ] 1 error, listed below:
[  ERROR   ] spec/03-plugins/01-tcp-log/01-tcp-log_spec.lua:787: Plugin: tcp-log (log) ssl_verify = true, [#postgres]  lazy_setup

 1 FAILED TEST
 1 ERROR"

exit 128
