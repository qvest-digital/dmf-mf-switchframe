# Changelog

## [1.0.0-rc.28](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.27...mxl-v1.0.0-rc.28) (2026-10-28)


### Bug Fixes

* **clip:** decode clips frame-threaded so they play in real time ([#102](https://github.com/qvest-digital/dmf-mf-switchframe/issues/102)) ([4b1d0ff](https://github.com/qvest-digital/dmf-mf-switchframe/commit/4b1d0ffea065d508b1c4655483d377ab323a5d01))
* **playout:** start a stopped or completed channel again ([#99](https://github.com/qvest-digital/dmf-mf-switchframe/issues/99)) ([ffd71f0](https://github.com/qvest-digital/dmf-mf-switchframe/commit/ffd71f0d9302d8af3290174085f9388ff3477b0d))
* **ui:** bound the video decode backlog by time, not by chunk count ([#106](https://github.com/qvest-digital/dmf-mf-switchframe/issues/106)) ([f5f9146](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f5f9146e933fce0c0cf35701b550d3bb2803b08f))
* **ui:** keep the program audio of a click made during page load ([#104](https://github.com/qvest-digital/dmf-mf-switchframe/issues/104)) ([6dfb7f4](https://github.com/qvest-digital/dmf-mf-switchframe/commit/6dfb7f42d0d1daac542d0ea61dfca1cc799eac73))

## [1.0.0-rc.27](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.26...mxl-v1.0.0-rc.27) (2026-10-28)


### Bug Fixes

* **ui:** stop requests subscribing their effect to engine health ([#96](https://github.com/qvest-digital/dmf-mf-switchframe/issues/96)) ([d1be55e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d1be55ef1616a5ff956ab0ef40fe950d49db6da6))

## [1.0.0-rc.26](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.25...mxl-v1.0.0-rc.26) (2026-10-28)


### Features

* **nmos:** state BCP-004-02 sender caps on MXL program senders ([#79](https://github.com/qvest-digital/dmf-mf-switchframe/issues/79)) ([830d4d8](https://github.com/qvest-digital/dmf-mf-switchframe/commit/830d4d8bd0729316d695256339c3a3fd26d50024))
* **nmos:** state BCP-004-02 sender caps on ST 2110 program senders ([#81](https://github.com/qvest-digital/dmf-mf-switchframe/issues/81)) ([7f75665](https://github.com/qvest-digital/dmf-mf-switchframe/commit/7f75665f06a668613b7ffb8bfea7e7ec03ec7ba8))


### Bug Fixes

* **nmos:** record a successful sender activation as active again ([#87](https://github.com/qvest-digital/dmf-mf-switchframe/issues/87)) ([80a269d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/80a269d55ba0444f9d38fa86af1d875e5de54e73))
* **nmos:** republish the ST 2110 program Flow on a format change ([#82](https://github.com/qvest-digital/dmf-mf-switchframe/issues/82)) ([74dcd31](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74dcd31ecd5ec5e4816cb6f42d96b4a61bbf2ba1))
* **prism:** let a keyframe displace a full viewer queue ([#90](https://github.com/qvest-digital/dmf-mf-switchframe/issues/90)) ([3689a48](https://github.com/qvest-digital/dmf-mf-switchframe/commit/3689a48d839af15f540ade30934065fa781e8b14))
* **switcher:** give every state snapshot a later timestamp ([c0b59d3](https://github.com/qvest-digital/dmf-mf-switchframe/commit/c0b59d38ba5c4e853a26e89f91e3e73e5b755805))
* **ui:** close video frames no canvas shows and stop headless decode ([#83](https://github.com/qvest-digital/dmf-mf-switchframe/issues/83)) ([0be290a](https://github.com/qvest-digital/dmf-mf-switchframe/commit/0be290aa706ef823c920e90df5bbf06f3843e2f0))
* **ui:** decode every source's audio into one AudioContext per engine ([#86](https://github.com/qvest-digital/dmf-mf-switchframe/issues/86)) ([20fc8b1](https://github.com/qvest-digital/dmf-mf-switchframe/commit/20fc8b16ce7beef8376553515a0046c458398230))
* **ui:** ignore a state snapshot older than an engine's last one ([c0b59d3](https://github.com/qvest-digital/dmf-mf-switchframe/commit/c0b59d38ba5c4e853a26e89f91e3e73e5b755805))
* **ui:** keep tiles and preview on the engine a failover moved them to ([#85](https://github.com/qvest-digital/dmf-mf-switchframe/issues/85)) ([094585c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/094585cb8c354d8f3bbd8c3b0090981985fd0222))
* **ui:** leave no audio decoder or context behind a destroyed pipeline ([#88](https://github.com/qvest-digital/dmf-mf-switchframe/issues/88)) ([8e63242](https://github.com/qvest-digital/dmf-mf-switchframe/commit/8e632423ad2a53f0da8031bc8f6ac258fba933be))
* **ui:** move canvases back to engine A when engine B's pipeline goes ([#94](https://github.com/qvest-digital/dmf-mf-switchframe/issues/94)) ([02c9314](https://github.com/qvest-digital/dmf-mf-switchframe/commit/02c93148dc63fc5b38eeaf3c0af52d5df3743466))
* **ui:** show the engine's upload progress in other browsers ([c0b59d3](https://github.com/qvest-digital/dmf-mf-switchframe/commit/c0b59d38ba5c4e853a26e89f91e3e73e5b755805))

## [1.0.0-rc.25](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.24...mxl-v1.0.0-rc.25) (2026-10-28)


### Features

* **mxl:** add --mxl-source-preset for the source relay encode ([#77](https://github.com/qvest-digital/dmf-mf-switchframe/issues/77)) ([f95705e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f95705e4438b8fd20fbd5f2a71a5cb71b8e6d23b))


### Bug Fixes

* **mxl:** keep the video reader a few grains behind the head ([#74](https://github.com/qvest-digital/dmf-mf-switchframe/issues/74)) ([85c42f8](https://github.com/qvest-digital/dmf-mf-switchframe/commit/85c42f84c50fd9f48d78a33d906fa3bf4d4d5a19))

## [1.0.0-rc.24](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.23...mxl-v1.0.0-rc.24) (2026-10-28)


### Features

* **mxl:** add --mxl-gc-interval to set or stop domain collection ([#72](https://github.com/qvest-digital/dmf-mf-switchframe/issues/72)) ([05bf843](https://github.com/qvest-digital/dmf-mf-switchframe/commit/05bf843ea3468c98b06b48f81f3e97c644f077e9))


### Bug Fixes

* **mxl:** run the relay encode beside MXL ingest, not in it ([#73](https://github.com/qvest-digital/dmf-mf-switchframe/issues/73)) ([23bd839](https://github.com/qvest-digital/dmf-mf-switchframe/commit/23bd83931ef0c35a48e86d6f462c80fc0c5dacdb))

## [1.0.0-rc.23](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.22...mxl-v1.0.0-rc.23) (2026-10-27)


### Features

* **stmap:** add --stmap-prewarm to skip the startup cache prewarm ([#70](https://github.com/qvest-digital/dmf-mf-switchframe/issues/70)) ([4c2dfdf](https://github.com/qvest-digital/dmf-mf-switchframe/commit/4c2dfdf799fef39500e7f6d903078061214cde7d))


### Bug Fixes

* **audio:** stop a catch-up burst when the mixer closes ([#66](https://github.com/qvest-digital/dmf-mf-switchframe/issues/66)) ([a1a3c89](https://github.com/qvest-digital/dmf-mf-switchframe/commit/a1a3c892c406768628c9087857750089990fcf62))

## [1.0.0-rc.22](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.21...mxl-v1.0.0-rc.22) (2026-10-26)


### Bug Fixes

* **nmos:** list no receivers as an empty array, not null ([#64](https://github.com/qvest-digital/dmf-mf-switchframe/issues/64)) ([f05cf85](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f05cf8524362c92550d5520e9e5c256d2b3543e5))
* **nmos:** take the node back from a follower that registered last ([#63](https://github.com/qvest-digital/dmf-mf-switchframe/issues/63)) ([d0ea1f6](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d0ea1f65be0119a06e110530f125a4ecc2af4c5b))

## [1.0.0-rc.21](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.20...mxl-v1.0.0-rc.21) (2026-10-26)


### Features

* **peer:** put the program on air at start, following a live peer ([#61](https://github.com/qvest-digital/dmf-mf-switchframe/issues/61)) ([561d5cf](https://github.com/qvest-digital/dmf-mf-switchframe/commit/561d5cfafcaa9d01178f84a84860901817172728))

## [1.0.0-rc.20](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.19...mxl-v1.0.0-rc.20) (2026-10-23)


### Bug Fixes

* **ui:** push from the newer engine when an auto push is stale ([#59](https://github.com/qvest-digital/dmf-mf-switchframe/issues/59)) ([2c6edcc](https://github.com/qvest-digital/dmf-mf-switchframe/commit/2c6edcc97d5538a12325f3b7f3f746d769540df6))
* **ui:** schedule timed commands at a whole microsecond ([#58](https://github.com/qvest-digital/dmf-mf-switchframe/issues/58)) ([0d23294](https://github.com/qvest-digital/dmf-mf-switchframe/commit/0d23294f8f4753c180052049f536a10bca4dd919))

## [1.0.0-rc.19](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.18...mxl-v1.0.0-rc.19) (2026-10-23)


### Bug Fixes

* **fastctrl:** deliver clock-sync pongs to the browser ([44de68c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/44de68cb05c662e4939b51b359c510c6cbf5df43))
* **sync:** take the snapshot's command seq when applying it ([44de68c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/44de68cb05c662e4939b51b359c510c6cbf5df43))
* **ui:** compare audio mix settings across engines, not peak meters ([#49](https://github.com/qvest-digital/dmf-mf-switchframe/issues/49)) ([e62dfd5](https://github.com/qvest-digital/dmf-mf-switchframe/commit/e62dfd5a9750e66d2de3fc0a227602c8bdc50461))
* **ui:** restart the RTT estimate after a delayed first pong ([#57](https://github.com/qvest-digital/dmf-mf-switchframe/issues/57)) ([e7c4f7b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/e7c4f7b1813d74cd465d8e43218ce4694ab38fc3))
* **ui:** send one unique command seq per command to both engines ([#52](https://github.com/qvest-digital/dmf-mf-switchframe/issues/52)) ([30ea907](https://github.com/qvest-digital/dmf-mf-switchframe/commit/30ea90729a98cfb25d2c5ae6ae49b8bede7806b7))
* **ui:** send the command seq with commands that run on arrival ([44de68c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/44de68cb05c662e4939b51b359c510c6cbf5df43))
* **ui:** show the measured command latency per engine ([#50](https://github.com/qvest-digital/dmf-mf-switchframe/issues/50)) ([0ae2207](https://github.com/qvest-digital/dmf-mf-switchframe/commit/0ae22074ba15306dbc5aebe4f3b2e84471753f31))
* **ui:** take the reconcile locks for a manual engine push ([#51](https://github.com/qvest-digital/dmf-mf-switchframe/issues/51)) ([522d89a](https://github.com/qvest-digital/dmf-mf-switchframe/commit/522d89a32e8b564f11aedb9dbbca9763e09fedff))

## [1.0.0-rc.18](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.17...mxl-v1.0.0-rc.18) (2026-10-23)


### Bug Fixes

* **nmos:** take over every resource when re-registering the node ([#46](https://github.com/qvest-digital/dmf-mf-switchframe/issues/46)) ([6d64083](https://github.com/qvest-digital/dmf-mf-switchframe/commit/6d64083727793459c27b000bc3a719595c21c193))

## [1.0.0-rc.17](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.16...mxl-v1.0.0-rc.17) (2026-10-23)


### Bug Fixes

* **output:** give each SRT reconnect loop its own done channel ([#45](https://github.com/qvest-digital/dmf-mf-switchframe/issues/45)) ([cd24b2f](https://github.com/qvest-digital/dmf-mf-switchframe/commit/cd24b2f936a8362d572b178452f46f7a80d46d7f))

## [1.0.0-rc.16](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.15...mxl-v1.0.0-rc.16) (2026-10-23)


### Bug Fixes

* **nmos:** keep a pair registered while its leader is down ([#43](https://github.com/qvest-digital/dmf-mf-switchframe/issues/43)) ([194f062](https://github.com/qvest-digital/dmf-mf-switchframe/commit/194f062780244d5c628b2aa2edc27ebd58e94718))

## [1.0.0-rc.15](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.14...mxl-v1.0.0-rc.15) (2026-10-22)


### Bug Fixes

* **state:** broadcast the clip a player is loading ([#40](https://github.com/qvest-digital/dmf-mf-switchframe/issues/40)) ([87c4fb9](https://github.com/qvest-digital/dmf-mf-switchframe/commit/87c4fb95f0f7269a15ad48432e2a49ed60b2ffe2))

## [1.0.0-rc.14](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.13...mxl-v1.0.0-rc.14) (2026-10-22)


### Features

* **nmos:** offer both engines' programs of a pair as senders ([#37](https://github.com/qvest-digital/dmf-mf-switchframe/issues/37)) ([ce2c10e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/ce2c10e43f6c56c556ab83eb5f3700953f5daad8))


### Bug Fixes

* **mxl:** keep the old flow until the new one is written on the node ([#38](https://github.com/qvest-digital/dmf-mf-switchframe/issues/38)) ([9a15b34](https://github.com/qvest-digital/dmf-mf-switchframe/commit/9a15b34676e1ac1ea2419f4756ea152caabe6c74))

## [1.0.0-rc.13](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.12...mxl-v1.0.0-rc.13) (2026-10-22)


### Bug Fixes

* **auth:** keep the API token out of logs and stdout ([#35](https://github.com/qvest-digital/dmf-mf-switchframe/issues/35)) ([3c4b606](https://github.com/qvest-digital/dmf-mf-switchframe/commit/3c4b60675f92b3a6b5626583eb99a801c5f11bfb))
* **clip:** keep clip uploads and loads in step across engines ([#34](https://github.com/qvest-digital/dmf-mf-switchframe/issues/34)) ([3c9c205](https://github.com/qvest-digital/dmf-mf-switchframe/commit/3c9c2050ba49eb0d01b1b8c699a9998980bc0fdd))

## [1.0.0-rc.12](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.11...mxl-v1.0.0-rc.12) (2026-10-22)


### Bug Fixes

* **clip:** keep clip players and clip IDs in step across engines ([#29](https://github.com/qvest-digital/dmf-mf-switchframe/issues/29)) ([9c17b10](https://github.com/qvest-digital/dmf-mf-switchframe/commit/9c17b10b693ff9a42cd0161e5ae42ad9d02c2aea))
* **nmos:** register the program resources a registry accepts ([#33](https://github.com/qvest-digital/dmf-mf-switchframe/issues/33)) ([cc4080b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/cc4080b3d8aad0f10667e0969397824ec8f0c93b))

## [1.0.0-rc.11](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.10...mxl-v1.0.0-rc.11) (2026-10-22)


### Features

* **mxl:** move a named input to other flows without ending its stream ([513aa7b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/513aa7b93120fd4abf2096c286ae4fbcf5c1f55e))
* **nmos:** add BCP-007-03 MXL connection management ([513aa7b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/513aa7b93120fd4abf2096c286ae4fbcf5c1f55e))
* **nmos:** expose the MXL program output as senders ([513aa7b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/513aa7b93120fd4abf2096c286ae4fbcf5c1f55e))
* **nmos:** let a controller connect named MXL inputs over BCP-007-03 ([513aa7b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/513aa7b93120fd4abf2096c286ae4fbcf5c1f55e))
* **ui:** leave out inputs no flow is connected to ([513aa7b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/513aa7b93120fd4abf2096c286ae4fbcf5c1f55e))

## [1.0.0-rc.10](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.9...mxl-v1.0.0-rc.10) (2026-10-22)


### Bug Fixes

* **ui:** fetch the invite tokens once per director session ([#26](https://github.com/qvest-digital/dmf-mf-switchframe/issues/26)) ([5c4e971](https://github.com/qvest-digital/dmf-mf-switchframe/commit/5c4e971cf1aa9ffadea98636de6a333921509422))
* **ui:** keep the preview on engine A when viewing engine B's program ([#27](https://github.com/qvest-digital/dmf-mf-switchframe/issues/27)) ([3171698](https://github.com/qvest-digital/dmf-mf-switchframe/commit/317169845f262c7d7aed5ca9433400730b597226))

## [1.0.0-rc.9](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.8...mxl-v1.0.0-rc.9) (2026-10-22)


### Features

* **auth:** sign operators in with OpenID Connect ([#24](https://github.com/qvest-digital/dmf-mf-switchframe/issues/24)) ([112af11](https://github.com/qvest-digital/dmf-mf-switchframe/commit/112af11896632c48cbaf84e4ef4d44e6d0c58f9a))

## [1.0.0-rc.8](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.7...mxl-v1.0.0-rc.8) (2026-10-22)


### Bug Fixes

* **operator:** register one operator token on both engines ([#23](https://github.com/qvest-digital/dmf-mf-switchframe/issues/23)) ([4efa6e9](https://github.com/qvest-digital/dmf-mf-switchframe/commit/4efa6e9eb20d3d239c7fe388ef110fd9184d47ff))


### Miscellaneous

* keep the MPEG-TS clips out of the code graph ([#21](https://github.com/qvest-digital/dmf-mf-switchframe/issues/21)) ([f89618f](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f89618f7d1ae11b4b984647d5a17b938fccf0d07))

## [1.0.0-rc.7](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.6...mxl-v1.0.0-rc.7) (2026-10-22)


### Bug Fixes

* **ui:** keep both Full Compare canvases attached to their engines ([4b12b9b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/4b12b9bbee6690a1a29251da0279b7cf29dfe219))
* **ui:** leave live meters and local timestamps out of the comparison ([12843ac](https://github.com/qvest-digital/dmf-mf-switchframe/commit/12843ac5eef4a7c065058a848f64689896619013))
* **ui:** let a command reach both engines before calling them diverged ([12843ac](https://github.com/qvest-digital/dmf-mf-switchframe/commit/12843ac5eef4a7c065058a848f64689896619013))
* **ui:** stop the redundancy panel's thumbnail poll restarting on each tick ([4b12b9b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/4b12b9bbee6690a1a29251da0279b7cf29dfe219))
* **ui:** treat a missing API token as a token prompt, not engine A down ([#20](https://github.com/qvest-digital/dmf-mf-switchframe/issues/20)) ([e8fc605](https://github.com/qvest-digital/dmf-mf-switchframe/commit/e8fc605541d560aafcef5c490d1fce1e809015e7))


### Build System

* **deps:** move Dockerfile.mxl to go-mxl 1.1.0-rc.7 ([#16](https://github.com/qvest-digital/dmf-mf-switchframe/issues/16)) ([5b6266c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/5b6266c87036053b5a73fa17bf642beab34e2ef4))

## [1.0.0-rc.6](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.5...mxl-v1.0.0-rc.6) (2026-10-21)


### Bug Fixes

* **snapshot:** apply an empty layout as no layout ([#14](https://github.com/qvest-digital/dmf-mf-switchframe/issues/14)) ([ebb30be](https://github.com/qvest-digital/dmf-mf-switchframe/commit/ebb30bea889f095858dc574ab1833dfa466cd809))

## [1.0.0-rc.5](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.4...mxl-v1.0.0-rc.5) (2026-10-21)


### Bug Fixes

* **ui:** stop dual-engine failover flapping ([#12](https://github.com/qvest-digital/dmf-mf-switchframe/issues/12)) ([81d1882](https://github.com/qvest-digital/dmf-mf-switchframe/commit/81d18821f96d516e518938239129f2a45b8d269b))

## [1.0.0-rc.4](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.3...mxl-v1.0.0-rc.4) (2026-10-21)


### Features

* **peer:** tell the browser the peer's public origin ([#11](https://github.com/qvest-digital/dmf-mf-switchframe/issues/11)) ([0717107](https://github.com/qvest-digital/dmf-mf-switchframe/commit/0717107eba74398e55791b3d8ccc14381601289e))


### Bug Fixes

* **ui:** open every WebTransport session where the engine advertises it ([#9](https://github.com/qvest-digital/dmf-mf-switchframe/issues/9)) ([95cacb6](https://github.com/qvest-digital/dmf-mf-switchframe/commit/95cacb6a2ade3358d6213d2a8a081d68ebfe36c9))

## [1.0.0-rc.3](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.2...mxl-v1.0.0-rc.3) (2026-10-21)


### Features

* **http:** advertise a WebTransport host apart from the UI's ([#7](https://github.com/qvest-digital/dmf-mf-switchframe/issues/7)) ([17f5b04](https://github.com/qvest-digital/dmf-mf-switchframe/commit/17f5b04818c684cb307939302481bef7bb27851d))


### Bug Fixes

* **codec:** choose H.264 and HEVC levels from the stream's rate ([60767a8](https://github.com/qvest-digital/dmf-mf-switchframe/commit/60767a8fa2c233711b78485ec34104d235d21912))
* **gpu:** pass the fallback encoder a GOP in seconds ([60767a8](https://github.com/qvest-digital/dmf-mf-switchframe/commit/60767a8fa2c233711b78485ec34104d235d21912))

## [1.0.0-rc.2](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.1...mxl-v1.0.0-rc.2) (2026-10-21)


### Features

* **http:** serve HTTPS with the generated certificate on request ([#4](https://github.com/qvest-digital/dmf-mf-switchframe/issues/4)) ([80b6da9](https://github.com/qvest-digital/dmf-mf-switchframe/commit/80b6da9698c660eef9810b216e6e42d073dbbdeb))

## [1.0.0-rc.1](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.0...mxl-v1.0.0-rc.1) (2026-10-21)


### Features

* **output:** read an RTMP destination's URL from urlFile ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
* **output:** start outputs declared in --outputs-config ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))


### Bug Fixes

* **gpu:** link the C++ runtime for CUDA builds ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
* **mxl:** keep data grain indices increasing ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
* **mxl:** keep program video grain indices contiguous ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
* **mxl:** move the 10-bit V210 kernels out of the cgo package ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
* **mxl:** reopen an input flow its writer recreated ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))


### Build System

* publish MXL images for CPU and CUDA ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))


### Continuous Integration

* cut releases with release-please ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
* drop upstream image and benchmark publishing ([d6c145d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d6c145ddc72b92073b2c1d8188f0b7ef6225ed72))
