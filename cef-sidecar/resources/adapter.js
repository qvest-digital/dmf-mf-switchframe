(function() {
    'use strict';

    window.__switchframe = {
        format: 'unknown',
        element: null,

        detect: function() {
            // Check OGraf: look for link[rel="ograf-manifest"] or custom element
            // with playAction method.
            var manifests = document.querySelectorAll('link[rel="ograf-manifest"]');
            if (manifests.length > 0) {
                this.format = 'ograf';
                // Find the OGraf custom element (tag name contains a hyphen).
                var allElements = document.querySelectorAll('body *');
                for (var i = 0; i < allElements.length; i++) {
                    var el = allElements[i];
                    if (el.tagName && el.tagName.includes('-') &&
                        typeof el.playAction === 'function') {
                        this.element = el;
                        break;
                    }
                }
                if (!this.element) {
                    // Try custom elements that might not have loaded yet.
                    var customs = document.querySelectorAll('[is]');
                    for (var j = 0; j < customs.length; j++) {
                        if (typeof customs[j].playAction === 'function') {
                            this.element = customs[j];
                            break;
                        }
                    }
                }
                // If manifest exists but no OGraf element found, fall back to CasparCG
                // globals if available (template has manifest but uses CasparCG API).
                if (!this.element && typeof window.play === 'function') {
                    this.format = 'casparcg';
                }
            } else if (typeof window.play === 'function') {
                this.format = 'casparcg';
            }

            this.reportFormat(this.format);
        },

        reportFormat: function(fmt) {
            // CefV8 binding sends this back to C++ (wired in adapter.cpp).
            if (window.__switchframe_native &&
                window.__switchframe_native.reportFormat) {
                window.__switchframe_native.reportFormat(fmt);
            }
        },

        play: function() {
            if (this.format === 'ograf' && this.element) {
                return this.element.playAction({});
            } else if (this.format === 'casparcg' &&
                       typeof window.play === 'function') {
                window.play();
            }
        },

        stop: function() {
            if (this.format === 'ograf' && this.element) {
                return this.element.stopAction({});
            } else if (this.format === 'casparcg' &&
                       typeof window.stop === 'function') {
                window.stop();
            }
        },

        next: function() {
            if (this.format === 'ograf' && this.element) {
                return this.element.playAction({ delta: 1 });
            } else if (this.format === 'casparcg' &&
                       typeof window.next === 'function') {
                window.next();
            }
        },

        update: function(data) {
            var dataStr = (typeof data === 'string') ? data : JSON.stringify(data);
            if (this.format === 'ograf' && this.element) {
                var parsed = (typeof data === 'string') ? JSON.parse(data) : data;
                return this.element.updateAction({ data: parsed });
            } else if (this.format === 'casparcg' &&
                       typeof window.update === 'function') {
                window.update(dataStr);
            }
        },

        custom: function(action, payload) {
            if (this.format === 'ograf' && this.element) {
                var parsedPayload = (typeof payload === 'string') ?
                    JSON.parse(payload) : payload;
                return this.element.customAction({
                    id: action,
                    payload: parsedPayload
                });
            }
            // CasparCG has no custom action equivalent.
        }
    };

    // Detect format after DOM is fully loaded.
    if (document.readyState === 'complete') {
        setTimeout(function() { window.__switchframe.detect(); }, 0);
    } else {
        window.addEventListener('load', function() {
            setTimeout(function() { window.__switchframe.detect(); }, 100);
        });
    }
})();
