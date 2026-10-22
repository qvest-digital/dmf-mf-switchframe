# Changelog

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
