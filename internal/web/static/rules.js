(function () {
	'use strict';

	if (window.__altRules) return;
	window.__altRules = 1;

	var presets = {};
	var pickField = 'chat';

	function form() { return document.querySelector('[data-rule-form]'); }

	function changed() {
		var f = form();
		if (f) f.dispatchEvent(new Event('rules:changed', { bubbles: true }));
	}

	function rememberPresets() {
		document.querySelectorAll('[data-rule-preset]:checked').forEach(function (r) { presets[r.name] = r.value; });
	}

	function toast(message, undo) {
		var f = form();
		var host = document.getElementById('hx-notice');
		if (!f || !host) return;
		var el = document.createElement('div');
		el.innerHTML = '<div role="status" class="alert alert-soft flex w-full items-center gap-2 text-sm shadow-md" data-undo-toast data-undo-ms="8000"><span class="min-w-0 flex-1"></span><button type="button" class="btn btn-sm max-lg:btn-md"></button></div>';
		el = el.firstChild;
		el.firstChild.textContent = message;
		var button = el.lastChild;
		button.textContent = f.dataset.undoLabel;
		button.addEventListener('click', function () { el.remove(); undo(); changed(); });
		host.append(el);
		document.dispatchEvent(new Event('undo:scan'));
	}

	function addHidden(container, name, value) {
		if (!container) return false;
		var dup = Array.prototype.some.call(container.querySelectorAll('input[type=hidden]'), function (i) { return i.name === name && i.value === value; });
		if (dup) return false;
		var input = Object.assign(document.createElement('input'), { type: 'hidden', name: name, value: value });
		container.prepend(input);
		return true;
	}

	function addWord(block) {
		var input = block.querySelector('[data-rule-word]');
		var word = input && input.value.trim();
		if (!word) return;
		input.value = '';
		if (addHidden(block.querySelector('[data-rule-words]'), 'text.' + block.dataset.ruleText + '.value', word)) changed();
	}

	function nextTextIndex() {
		var max = -1;
		document.querySelectorAll('#rule-blocks [data-rule-text]').forEach(function (b) { max = Math.max(max, Number(b.dataset.ruleText)); });
		return max + 1;
	}

	function removeWithUndo(nodes, message) {
		var anchor = nodes[nodes.length - 1].nextSibling;
		var parent = nodes[0].parentNode;
		nodes.forEach(function (n) { n.remove(); });
		changed();
		toast(message, function () { nodes.forEach(function (n) { parent.insertBefore(n, anchor); }); });
	}

	document.addEventListener('click', function (e) {
		var f = form();
		if (!f) return;
		var t = e.target;
		var chipRemove = t.closest('[data-rule-chip-remove]');
		if (chipRemove) {
			var chip = chipRemove.closest('[data-rule-chip]');
			var sep = chip.previousElementSibling && chip.previousElementSibling.matches('span:not([data-rule-chip])') ? chip.previousElementSibling : null;
			removeWithUndo(sep ? [sep, chip] : [chip], f.dataset.undoRemoved);
			return;
		}
		if (t.closest('[data-rule-word-add]')) { addWord(t.closest('[data-rule-text]')); return; }
		var textRemove = t.closest('[data-rule-text-remove]');
		if (textRemove) {
			var block = textRemove.closest('[data-rule-text]');
			var and = block.previousElementSibling;
			removeWithUndo(and ? [and, block] : [block], f.dataset.undoRemoved);
			return;
		}
		var add = t.closest('[data-rule-add-text]');
		if (add) {
			var i = String(nextTextIndex());
			var tpl = document.getElementById('rule-text-template');
			var html = tpl.innerHTML.replace(/__i__/g, i);
			add.insertAdjacentHTML('beforebegin', html);
			var word = add.previousElementSibling.querySelector('[data-rule-word]');
			if (word) word.focus();
			changed();
			return;
		}
		var pick = t.closest('[data-rule-pick]');
		if (pick) {
			pickField = pick.dataset.rulePick;
			var field = document.querySelector('[data-rule-pick-field]');
			var q = document.querySelector('[data-rule-pick-q]');
			if (field) field.value = pickField;
			document.querySelectorAll('[data-rule-pick-help]').forEach(function (h) { h.hidden = h.dataset.rulePickHelp !== pickField; });
			if (q) { q.value = ''; q.dispatchEvent(new Event('rules:pick', { bubbles: true })); }
			return;
		}
		var value = t.closest('[data-rule-pick-value]');
		if (value) {
			var target = document.getElementById(pickField === 'chat' ? 'rule-chats' : 'rule-senders');
			addHidden(target, pickField, value.dataset.rulePickValue);
			var dialog = value.closest('dialog');
			if (dialog) dialog.close();
			changed();
		}
	});

	document.addEventListener('keydown', function (e) {
		if (e.key !== 'Enter' || !e.target.matches('[data-rule-word]')) return;
		e.preventDefault();
		addWord(e.target.closest('[data-rule-text]'));
	});

	document.addEventListener('change', function (e) {
		var r = e.target;
		if (!e.isTrusted || !r.matches || !r.matches('#rule-blocks [data-rule-preset]')) return;
		var before = presets[r.name];
		presets[r.name] = r.value;
		var f = form();
		if (!f || before === undefined || before === r.value) return;
		toast(f.dataset.undoChanged, function () {
			var prev = f.querySelector('[data-rule-preset][name="' + r.name + '"][value="' + before + '"]');
			if (prev) { prev.checked = true; presets[r.name] = before; }
		});
	});

	document.addEventListener('submit', function (e) {
		if (!e.target.matches('[data-rule-form]')) return;
		var save = e.target.querySelector('[data-rule-save]');
		if (!save) return;
		save.disabled = true;
		var spin = save.querySelector('[data-rule-saving]');
		if (spin) spin.classList.remove('hidden');
	});

	document.addEventListener('htmx:after:swap', rememberPresets);
	if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', rememberPresets);
	else rememberPresets();
})();
