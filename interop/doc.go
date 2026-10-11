// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package interop holds tests only: telegrams built by the simulated
// wireless meter of github.com/cB-Sigma/mbus-sim (security modes 0, 5
// and 7, with and without the extended link layer of a bidirectional
// meter) are decoded by wmbusmeters, an independent wM-Bus decoder
// (GPL-3.0-or-later), and its JSON output is compared with what the
// simulator encoded.
//
// wmbusmeters runs in its published Docker image, pinned by tag and
// digest, with no network; the telegrams reach it as a simulation file
// in a read-only mounted temporary directory. Nothing of wmbusmeters is
// kept in this repository. The tests run only with SIGMA_MBUS_INTEROP=1
// and docker on PATH; they pull the image when it is missing (and skip
// when that fails) and leave it in place, so remove it afterwards with
// "docker image rm wmbusmeters/wmbusmeters:release-3.0.0" if it was
// pulled for them. The module is not part of the published library and
// nothing imports it.
package interop
