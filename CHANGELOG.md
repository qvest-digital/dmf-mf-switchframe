# Changelog

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
