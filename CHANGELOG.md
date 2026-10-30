# Changelog

## [1.0.0-rc.36](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.35...mxl-v1.0.0-rc.36) (2026-10-30)


### Bug Fixes

* **playout:** arm a live item's source when the cursor jumps to it ([8c5cfc8](https://github.com/qvest-digital/dmf-mf-switchframe/commit/8c5cfc88d1246b3f0289ca0437b3426382e39171))
* **switcher:** stop allocating a picture per frame in the live tap ([8c5cfc8](https://github.com/qvest-digital/dmf-mf-switchframe/commit/8c5cfc88d1246b3f0289ca0437b3426382e39171))

## [1.0.0-rc.35](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.34...mxl-v1.0.0-rc.35) (2026-10-29)


### Features

* **htmlgfx:** add an ograf-host template for manifest-only graphics ([74a7598](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74a7598ab566a4a20ef332ffc9f4d99596ab5521))
* **htmlgfx:** build the CEF sidecar into the MXL images ([f221e2e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f221e2e0479371e8ba9a32cfc159f6cb6b708dd6))
* **htmlgfx:** count sidecar frames that reach the compositor ([f221e2e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f221e2e0479371e8ba9a32cfc159f6cb6b708dd6))
* **htmlgfx:** report template failures as load errors ([74a7598](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74a7598ab566a4a20ef332ffc9f4d99596ab5521))
* **htmlgfx:** spawn the CEF sidecar only with --html5-graphics=true ([f221e2e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f221e2e0479371e8ba9a32cfc159f6cb6b708dd6))


### Bug Fixes

* **cef:** hold every navigation and load to the URL policy ([74a7598](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74a7598ab566a4a20ef332ffc9f4d99596ab5521))
* **cef:** select headless Ozone and stop background networking ([f221e2e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f221e2e0479371e8ba9a32cfc159f6cb6b708dd6))
* **htmlgfx:** ignore sidecar events for a freed slot ([74a7598](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74a7598ab566a4a20ef332ffc9f4d99596ab5521))
* **htmlgfx:** refuse graphic URLs other than https and templates ([74a7598](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74a7598ab566a4a20ef332ffc9f4d99596ab5521))
* **htmlgfx:** send a graphic's data again once its format is known ([74a7598](https://github.com/qvest-digital/dmf-mf-switchframe/commit/74a7598ab566a4a20ef332ffc9f4d99596ab5521))
* **playout:** air a live item's input on an engine without a GPU ([374f0be](https://github.com/qvest-digital/dmf-mf-switchframe/commit/374f0be1fb067893366057f7893dbf014336e054))
* **playout:** arm a live item's source before it airs ([374f0be](https://github.com/qvest-digital/dmf-mf-switchframe/commit/374f0be1fb067893366057f7893dbf014336e054))
* **switcher:** warn when a live item's source is not 8-bit ([374f0be](https://github.com/qvest-digital/dmf-mf-switchframe/commit/374f0be1fb067893366057f7893dbf014336e054))
* **ui:** list live playlist sources under their switcher label ([374f0be](https://github.com/qvest-digital/dmf-mf-switchframe/commit/374f0be1fb067893366057f7893dbf014336e054))
* **ui:** offer unconnected inputs and tell shared labels apart ([374f0be](https://github.com/qvest-digital/dmf-mf-switchframe/commit/374f0be1fb067893366057f7893dbf014336e054))


### Continuous Integration

* **mxl:** publish only the image digests the HTML5 smoke test ran ([f221e2e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f221e2e0479371e8ba9a32cfc159f6cb6b708dd6))

## [1.0.0-rc.34](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.33...mxl-v1.0.0-rc.34) (2026-10-29)


### Features

* **playout:** count the ticks the run loop makes up and skips ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))


### Bug Fixes

* **clip:** start every loop pass at the first keyframe ([#167](https://github.com/qvest-digital/dmf-mf-switchframe/issues/167)) ([c62afc0](https://github.com/qvest-digital/dmf-mf-switchframe/commit/c62afc0e3a369c5f02ea1463aa24c78e385fc3ae))
* **playout:** drop audio that ends before the first picture ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** drop the leading pictures of the first keyframe ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** give each pass of a clip without an IDR a fresh decoder ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** make up ticks the run loop misses ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** rewind a loop clip outside the channel lock ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** rewind looping clip items in place ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** rewind the idle loop in place ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))
* **playout:** start every loop pass at the first keyframe ([d32bcce](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d32bcce8c77c56bb84739c61955756182e1dd775))

## [1.0.0-rc.33](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.32...mxl-v1.0.0-rc.33) (2026-10-29)


### Features

* **metrics:** expose outputs, the recorder and SRT connections ([270f298](https://github.com/qvest-digital/dmf-mf-switchframe/commit/270f2986ba3e6c9fc24089c491d3e1fe62497323))
* **metrics:** expose playout, clip players, replay and graphics ([cadf047](https://github.com/qvest-digital/dmf-mf-switchframe/commit/cadf047bfc3b47f7893b20cab6ff601f35231abc))
* **metrics:** expose the engine pair and the NMOS registration ([ff0fe2f](https://github.com/qvest-digital/dmf-mf-switchframe/commit/ff0fe2f39b4ddb6180cf89219f42a0de6d9a0430))
* **metrics:** expose the program, its sources and the audio mix ([627299e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/627299e97f0ad0060770c437b2004c75fa1b0257))
* **mxl:** count what an MXL input's readers lose ([cad2779](https://github.com/qvest-digital/dmf-mf-switchframe/commit/cad27792a7c2771fb767078bdaebb8198e13ef1b))
* **output:** count the media time an RTMP output sends and its drops ([cad2779](https://github.com/qvest-digital/dmf-mf-switchframe/commit/cad27792a7c2771fb767078bdaebb8198e13ef1b))
* **output:** send each destination from one engine of a pair, fail over ([#148](https://github.com/qvest-digital/dmf-mf-switchframe/issues/148)) ([c20cae7](https://github.com/qvest-digital/dmf-mf-switchframe/commit/c20cae74b13240cba737761d82162d219ff7f0bd))


### Bug Fixes

* **control:** log successful reads at debug, collapse repeated failures ([#144](https://github.com/qvest-digital/dmf-mf-switchframe/issues/144)) ([cd3f45f](https://github.com/qvest-digital/dmf-mf-switchframe/commit/cd3f45f4bf4f2f8584b07f5032e1365cbb2d0368))
* **output:** delay RTMP video by the lip-sync offset like TS ([#155](https://github.com/qvest-digital/dmf-mf-switchframe/issues/155)) ([e562cc1](https://github.com/qvest-digital/dmf-mf-switchframe/commit/e562cc18499d7a327bdaf1608759c1e7414ae4c3))
* **playout:** report an item an engine cannot open as a pair mismatch ([#153](https://github.com/qvest-digital/dmf-mf-switchframe/issues/153)) ([b8c7ed0](https://github.com/qvest-digital/dmf-mf-switchframe/commit/b8c7ed0e1ca433a41b56d785cc2a29ed616cea4c))
* **prism:** cancel the context each stream is opened on ([#163](https://github.com/qvest-digital/dmf-mf-switchframe/issues/163)) ([31c16b4](https://github.com/qvest-digital/dmf-mf-switchframe/commit/31c16b41ab095f83d51b16362cf1a41166d0190f))
* **switcher:** use typed atomics for 64-bit counters ([#160](https://github.com/qvest-digital/dmf-mf-switchframe/issues/160)) ([af60f6a](https://github.com/qvest-digital/dmf-mf-switchframe/commit/af60f6a7822139710a11c80a98014c05964934ea))
* **ui:** hold the reload while preset, label and IO forms hold input ([#161](https://github.com/qvest-digital/dmf-mf-switchframe/issues/161)) ([93fcf64](https://github.com/qvest-digital/dmf-mf-switchframe/commit/93fcf64392c77e1cde9e7e0b1d130b2e093eb3e5))
* **ui:** reload a tab left on a build the engine no longer serves ([#145](https://github.com/qvest-digital/dmf-mf-switchframe/issues/145)) ([256590d](https://github.com/qvest-digital/dmf-mf-switchframe/commit/256590d921f0c910d431f5f18932aac83bde12d6))

## [1.0.0-rc.32](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.31...mxl-v1.0.0-rc.32) (2026-10-29)


### Features

* **control:** count rejected API requests under their route ([95e5ac1](https://github.com/qvest-digital/dmf-mf-switchframe/commit/95e5ac1c473a87ec39e6a69382205c7192a6905c))
* **output:** verify RTMPS server certificates by default ([#150](https://github.com/qvest-digital/dmf-mf-switchframe/issues/150)) ([5e8acb2](https://github.com/qvest-digital/dmf-mf-switchframe/commit/5e8acb2909bf259a7977f5139c4b5c1a802002fd))


### Bug Fixes

* **control:** allow PATCH in the CORS preflight answer ([#132](https://github.com/qvest-digital/dmf-mf-switchframe/issues/132)) ([33fe8b4](https://github.com/qvest-digital/dmf-mf-switchframe/commit/33fe8b44a46837e6500945fdae56c6dc113384b4))
* **metrics:** write the subsystem families nothing wrote ([95e5ac1](https://github.com/qvest-digital/dmf-mf-switchframe/commit/95e5ac1c473a87ec39e6a69382205c7192a6905c))
* **nmos:** keep IS-05 receiver connections across a restart ([#137](https://github.com/qvest-digital/dmf-mf-switchframe/issues/137)) ([2de317a](https://github.com/qvest-digital/dmf-mf-switchframe/commit/2de317a63ecd58182403162c31543edbaaef838d))
* **output:** bound RTMP I/O and back off failed connects ([#139](https://github.com/qvest-digital/dmf-mf-switchframe/issues/139)) ([f5371bd](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f5371bd51b7b2f969781d513e9c8d1e9e26be36f))
* **output:** keep RTMP timestamps monotonic across a 33-bit PTS wrap ([#151](https://github.com/qvest-digital/dmf-mf-switchframe/issues/151)) ([de087fd](https://github.com/qvest-digital/dmf-mf-switchframe/commit/de087fd2dcb096955173d5fbf15b0394184613c1))
* **output:** write RTMP timestamps in the FLV time base ([#138](https://github.com/qvest-digital/dmf-mf-switchframe/issues/138)) ([a235871](https://github.com/qvest-digital/dmf-mf-switchframe/commit/a235871fee4508e4cb38911a72ef44bfcf68d789))
* **preview:** encode every browser relay at preview size under proxy ([#140](https://github.com/qvest-digital/dmf-mf-switchframe/issues/140)) ([b458662](https://github.com/qvest-digital/dmf-mf-switchframe/commit/b4586621b23b7ddcc4580d6cf4b0ecac9a200aba))
* **replay:** name a replay's file after its clip ID ([#147](https://github.com/qvest-digital/dmf-mf-switchframe/issues/147)) ([233654b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/233654b24006847cf30eb24d9e0bc403c42d78d2))
* **replay:** store a replay under one clip ID on both engines ([#141](https://github.com/qvest-digital/dmf-mf-switchframe/issues/141)) ([9faf766](https://github.com/qvest-digital/dmf-mf-switchframe/commit/9faf7662c40e8597901d6dcf579526cfe6dc8bcd))
* **transition:** box pre-shrink a preview only from twice its size ([#142](https://github.com/qvest-digital/dmf-mf-switchframe/issues/142)) ([b8fcede](https://github.com/qvest-digital/dmf-mf-switchframe/commit/b8fcede33f438249a78481a114f0d3d96d514a16))
* **ui:** create layers, pods and SCTE-35 events under one ID per pair ([#143](https://github.com/qvest-digital/dmf-mf-switchframe/issues/143)) ([5a50b24](https://github.com/qvest-digital/dmf-mf-switchframe/commit/5a50b248af0b85f9318235db65439739ba4ace84))
* **ui:** keep engine-local history out of the dual-engine sync check ([#149](https://github.com/qvest-digital/dmf-mf-switchframe/issues/149)) ([7921463](https://github.com/qvest-digital/dmf-mf-switchframe/commit/7921463130123783a69a9ff4ee1adfe56fc8f2dc))
* **ui:** measure decode lag afresh after a drop and after a gap ([#136](https://github.com/qvest-digital/dmf-mf-switchframe/issues/136)) ([7f08e81](https://github.com/qvest-digital/dmf-mf-switchframe/commit/7f08e81842f464f1973531cea16be7b8d1938274))
* **ui:** name the action and error in the partial failure warning ([#133](https://github.com/qvest-digital/dmf-mf-switchframe/issues/133)) ([203a57e](https://github.com/qvest-digital/dmf-mf-switchframe/commit/203a57e61d2a1e22218253b8a6de752fa5bf8554))

## [1.0.0-rc.31](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.30...mxl-v1.0.0-rc.31) (2026-10-29)


### Bug Fixes

* **ui:** stop requests at a refused token and back polls off ([#130](https://github.com/qvest-digital/dmf-mf-switchframe/issues/130)) ([338f788](https://github.com/qvest-digital/dmf-mf-switchframe/commit/338f788e0d39f81ab9b1cbbdb8b6b20f95652e1d))

## [1.0.0-rc.30](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.29...mxl-v1.0.0-rc.30) (2026-10-29)


### Features

* **graphics:** keep layer settings on the engine, fix reorder, export layers ([#119](https://github.com/qvest-digital/dmf-mf-switchframe/issues/119)) ([75a331a](https://github.com/qvest-digital/dmf-mf-switchframe/commit/75a331ad45c3e07db3d5bd89000cb35db9719e63))
* **playout:** end a looping clip after its current pass ([#118](https://github.com/qvest-digital/dmf-mf-switchframe/issues/118)) ([900682c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/900682c23249e9256f4924c0571ca861e6634b6a))


### Bug Fixes

* **clip:** encode a clip player's browser relay at the preview size ([#115](https://github.com/qvest-digital/dmf-mf-switchframe/issues/115)) ([961c6c1](https://github.com/qvest-digital/dmf-mf-switchframe/commit/961c6c12f1a4bafbfc90146290955c492768db19))
* **clip:** keep a looping clip's cadence across the wrap ([#111](https://github.com/qvest-digital/dmf-mf-switchframe/issues/111)) ([4717077](https://github.com/qvest-digital/dmf-mf-switchframe/commit/4717077fa1eca554a5506e351853f3fb86895628))
* **clip:** output clips at the pipeline rate with a fixed cadence ([#110](https://github.com/qvest-digital/dmf-mf-switchframe/issues/110)) ([9305936](https://github.com/qvest-digital/dmf-mf-switchframe/commit/9305936638a3161477cead2e95234b27e9a661a9))
* **control:** list every engine's archived recordings for import ([#117](https://github.com/qvest-digital/dmf-mf-switchframe/issues/117)) ([aa047ac](https://github.com/qvest-digital/dmf-mf-switchframe/commit/aa047ac911c79591af3bc4c98235dab3d12d61aa))
* **dve:** refuse the DVE transitions the CPU cannot hold at 1080p50 ([#105](https://github.com/qvest-digital/dmf-mf-switchframe/issues/105)) ([78af18a](https://github.com/qvest-digital/dmf-mf-switchframe/commit/78af18a0e0624339f2c7d565808afac07c1f7b1f))
* **mxl:** write each program frame on arrival, one grain per frame ([#109](https://github.com/qvest-digital/dmf-mf-switchframe/issues/109)) ([1ff0b54](https://github.com/qvest-digital/dmf-mf-switchframe/commit/1ff0b5470063b8d968afa2a44c40bd28b4f00621))
* playout sync and recovery, graphics save follow-ups ([#122](https://github.com/qvest-digital/dmf-mf-switchframe/issues/122)) ([5511fd9](https://github.com/qvest-digital/dmf-mf-switchframe/commit/5511fd98c7a960fe7058597a4bfcd73b881fc0ef))
* **playout:** cue an item on a channel that is not on air ([#116](https://github.com/qvest-digital/dmf-mf-switchframe/issues/116)) ([52615a4](https://github.com/qvest-digital/dmf-mf-switchframe/commit/52615a4969d224fe883d7ab745ba13349e7947ef))
* **playout:** encode a playout channel's relay when no GPU does ([#114](https://github.com/qvest-digital/dmf-mf-switchframe/issues/114)) ([56ffded](https://github.com/qvest-digital/dmf-mf-switchframe/commit/56ffdedff0c3038398813d3b86bd97a1979cec14))
* **playout:** scale a playout relay's pictures off the channel goroutine ([#125](https://github.com/qvest-digital/dmf-mf-switchframe/issues/125)) ([5c48c76](https://github.com/qvest-digital/dmf-mf-switchframe/commit/5c48c76b18cc1c65b84c9c391541488b53ff1496))
* show recording states, report empty recordings, authenticate recording media ([#120](https://github.com/qvest-digital/dmf-mf-switchframe/issues/120)) ([d80cacb](https://github.com/qvest-digital/dmf-mf-switchframe/commit/d80cacb49ee66f7371bc0a44b9731ee683ce3b37))
* **switcher:** keep program output running while its source stalls ([#126](https://github.com/qvest-digital/dmf-mf-switchframe/issues/126)) ([9903b8c](https://github.com/qvest-digital/dmf-mf-switchframe/commit/9903b8c5b25be6679afa0ecdab70e723f46899e8))
* **switcher:** run a transition whose source has stalled ([#128](https://github.com/qvest-digital/dmf-mf-switchframe/issues/128)) ([eff7d16](https://github.com/qvest-digital/dmf-mf-switchframe/commit/eff7d16823d450393480b3bb3ae787727d5f127a))
* **transition:** draw CPU transition frames off the source goroutine ([#113](https://github.com/qvest-digital/dmf-mf-switchframe/issues/113)) ([817df36](https://github.com/qvest-digital/dmf-mf-switchframe/commit/817df36bcf00de663afd37cbab64ee8ad1b52646))
* **ui:** make PFL/AFL route a channel to the monitor, add monitor control ([#121](https://github.com/qvest-digital/dmf-mf-switchframe/issues/121)) ([75e7d9f](https://github.com/qvest-digital/dmf-mf-switchframe/commit/75e7d9fba0696620c2e3d33baea59e95c0393a18))
* **ui:** reconnect a source's MoQ transport after a network drop ([#124](https://github.com/qvest-digital/dmf-mf-switchframe/issues/124)) ([e47969b](https://github.com/qvest-digital/dmf-mf-switchframe/commit/e47969be951ec966aa9d11bfedc8ff64ece8bebb))
* **ui:** resend a control's newest value after an overtaken request ([#127](https://github.com/qvest-digital/dmf-mf-switchframe/issues/127)) ([e420a80](https://github.com/qvest-digital/dmf-mf-switchframe/commit/e420a80ab3dcf09fa287b4031d4fb1c729c1c59d))
* **ui:** stop dragged controls and engine lag from raising the sync banners ([#123](https://github.com/qvest-digital/dmf-mf-switchframe/issues/123)) ([f5fda83](https://github.com/qvest-digital/dmf-mf-switchframe/commit/f5fda83605d4fc54b4534ad94d83b5c1de23bbc5))

## [1.0.0-rc.29](https://github.com/qvest-digital/dmf-mf-switchframe/compare/mxl-v1.0.0-rc.28...mxl-v1.0.0-rc.29) (2026-10-28)


### Features

* **clip:** attach a shared clip library directory ([844ed22](https://github.com/qvest-digital/dmf-mf-switchframe/commit/844ed226ce10e3bbf8b75d7c4c7f3fb709ab59c9))
* **output:** archive finished recordings into a create-only directory ([844ed22](https://github.com/qvest-digital/dmf-mf-switchframe/commit/844ed226ce10e3bbf8b75d7c4c7f3fb709ab59c9))
* **ui:** confirm deleting a clip from the shared clip library ([844ed22](https://github.com/qvest-digital/dmf-mf-switchframe/commit/844ed226ce10e3bbf8b75d7c4c7f3fb709ab59c9))


### Bug Fixes

* **colorgrade:** grade on the CPU when no GPU pipeline does ([#100](https://github.com/qvest-digital/dmf-mf-switchframe/issues/100)) ([44f4dec](https://github.com/qvest-digital/dmf-mf-switchframe/commit/44f4dec89590be34427b75604634ac52aae5ed9d))
* **dve:** draw 2D DVE transitions on the CPU when no GPU does ([#103](https://github.com/qvest-digital/dmf-mf-switchframe/issues/103)) ([1a7cf64](https://github.com/qvest-digital/dmf-mf-switchframe/commit/1a7cf642834d4f3112652870cf3384698c060d3f))

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
