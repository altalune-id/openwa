(function () {
	'use strict';

	if (window.__altPairing) return;
	window.__altPairing = 1;

	var totals = new Map();

	function tick() {
		var now = Date.now();
		document.querySelectorAll('[data-countdown-until]').forEach(function (el) {
			var until = Number(el.getAttribute('data-countdown-until'));
			if (!until) return;
			if (!totals.has(until)) totals.set(until, Math.max(until - now, 1000));
			var left = Math.max(0, until - now);
			var pct = Math.round((left / totals.get(until)) * 100);
			var secs = String(Math.ceil(left / 1000));
			el.style.setProperty('--value', String(pct));
			el.setAttribute('aria-valuenow', String(pct));
			el.closest('[data-countdown]').querySelectorAll('[data-countdown-left]').forEach(function (s) {
				s.textContent = secs;
			});
		});
	}

	var timer = null;
	function arm() {
		var live = document.querySelector('[data-countdown-until]');
		if (live && !timer) timer = setInterval(tick, 500);
		if (!live && timer) {
			clearInterval(timer);
			timer = null;
		}
		tick();
	}

	document.addEventListener('htmx:after:settle', arm);
	if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', arm);
	else arm();
})();
