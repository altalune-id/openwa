(function () {
	'use strict';

	if (window.__altLocaltime) return;
	window.__altLocaltime = 1;

	var lang = document.documentElement.lang || undefined;

	function fmt(at, tz, timeOnly) {
		var o = timeOnly ? { timeStyle: 'short' } : { dateStyle: 'medium', timeStyle: 'short', calendar: 'gregory' };
		if (tz) o.timeZone = tz;
		try {
			return new Intl.DateTimeFormat(lang, o).format(at);
		} catch (e) {
			return '';
		}
	}

	function offset(tz, at) {
		try {
			var o = { timeZoneName: 'shortOffset' };
			if (tz) o.timeZone = tz;
			var part = new Intl.DateTimeFormat('en-US', o).formatToParts(at).find(function (p) {
				return p.type === 'timeZoneName';
			});
			return part ? part.value : '';
		} catch (e) {
			return '';
		}
	}

	function apply() {
		document.querySelectorAll('time[data-local]').forEach(function (el) {
			var at = new Date(el.getAttribute('datetime'));
			if (isNaN(at)) return;
			el.textContent = fmt(at, el.getAttribute('data-tz'), el.getAttribute('data-local') === 'time') || el.textContent;
		});
		document.querySelectorAll('[data-zone-alt]').forEach(function (el) {
			var at = new Date(el.getAttribute('data-at'));
			var zone = offset(el.getAttribute('data-zone'), at);
			el.hidden = isNaN(at) || zone === '' || zone === offset('', at);
		});
	}

	if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', apply);
	else apply();
	document.addEventListener('htmx:after:settle', apply);
})();
