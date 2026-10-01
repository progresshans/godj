"use strict";

// The server remains authoritative for counts, identity, permission and writes.
// This enhancement only adds/removes unsaved DOM rows within declared bounds.
(() => {
    const integer = (value) => /^(0|[1-9][0-9]*)$/.test(value) && Number.isSafeInteger(Number(value)) ? Number(value) : null;
    for (const group of document.querySelectorAll("[data-inline-prefix]")) {
        const prefix = group.dataset.inlinePrefix;
        const form = group.closest("form");
        const container = group.querySelector(":scope > [data-inline-rows]");
        const prototype = group.querySelector(":scope > template[data-inline-empty]");
        const add = group.querySelector(":scope > [data-inline-add]");
        if (!form || !container || !prototype || !add) continue;
        const total = form.elements.namedItem(prefix + "-TOTAL_FORMS");
        const initial = form.elements.namedItem(prefix + "-INITIAL_FORMS");
        if (!(total instanceof HTMLInputElement) || !(initial instanceof HTMLInputElement)) continue;
        const saved = integer(initial.value);
        const minimum = integer(group.dataset.inlineMin);
        const maximum = integer(group.dataset.inlineMax);
        const rows = () => Array.from(container.children).filter((row) => row.hasAttribute("data-inline-row"));
        if (saved === null || minimum === null || maximum === null || minimum > maximum || saved > rows().length || integer(total.value) !== rows().length) continue;
        if (rows().some((row, index) => row.dataset.inlineRow !== String(index) || row.dataset.inlineRowPrefix !== prefix + "-" + index)) continue;

        // Replace only structural prefixes. User values, labels and option text
        // may themselves contain __prefix__ or an old row name; never edit them.
        const renumber = (row, index) => {
            const before = row.dataset.inlineRowPrefix;
            const after = prefix + "-" + index;
            for (const element of [row, ...row.querySelectorAll("*")]) {
                for (const attribute of ["name", "id", "for", "data-field-name", "data-error-field", "data-inline-error"]) {
                    const value = element.getAttribute(attribute);
                    if (value === before || value?.startsWith(before + "-")) {
                        element.setAttribute(attribute, after + value.slice(before.length));
                    }
                }
            }
            row.dataset.inlineRow = String(index);
            row.dataset.inlineRowPrefix = after;
            const number = row.querySelector("[data-inline-number]");
            if (number) number.textContent = String(index + 1);
            const remove = row.querySelector("[data-inline-remove]");
            if (remove) remove.setAttribute("aria-label", "Remove item " + (index + 1));
        };
        const refresh = () => {
            const current = rows();
            total.value = String(current.length);
            add.hidden = current.length >= maximum;
            for (const [index, row] of current.entries()) {
                const remove = row.querySelector("[data-inline-remove]");
                if (remove) remove.hidden = index < saved || current.length <= minimum;
            }
        };
        const focusRow = (row) => {
            const field = row?.querySelector("[data-inline-fields] input:not([type=hidden]):not(:disabled), [data-inline-fields] textarea:not(:disabled), [data-inline-fields] select:not(:disabled)");
            if (field) field.focus();
            else if (!add.hidden) add.focus();
        };
        add.addEventListener("click", () => {
            const count = rows().length;
            if (count >= maximum) return;
            const row = prototype.content.firstElementChild?.cloneNode(true);
            if (!row || row.dataset.inlineRowPrefix !== prefix + "-__prefix__") return;
            renumber(row, count);
            container.append(row);
            refresh();
            focusRow(row);
            row.dispatchEvent(new CustomEvent("formset:added", {bubbles: true, detail: {formsetName: prefix}}));
        });
        container.addEventListener("click", (event) => {
            if (!(event.target instanceof Element)) return;
            const button = event.target.closest("[data-inline-remove]");
            const row = button?.closest("[data-inline-row]");
            if (!row || row.parentElement !== container) return;
            const current = rows();
            const index = current.indexOf(row);
            if (index < saved || current.length <= minimum) return;
            row.remove();
            // Existing row identity/control names are untouched. Only the
            // remaining unsaved suffix needs contiguous management indexes.
            const remaining = rows();
            for (let position = saved; position < remaining.length; position++) renumber(remaining[position], position);
            refresh();
            focusRow(remaining[index] || remaining[index - 1]);
            group.dispatchEvent(new CustomEvent("formset:removed", {bubbles: true, detail: {formsetName: prefix}}));
        });
        refresh();
    }
})();
