"use strict";

// Search pages only change what the native select displays. Its string values
// remain the submitted values; the server resolves and authorizes them on save.
(() => {
    const states = new Map();
    const encoder = new TextEncoder();
    const integer = (value) => /^(0|[1-9][0-9]*)$/.test(value) && Number.isSafeInteger(Number(value)) ? Number(value) : null;
    const rowContext = (state) => {
        const row = state.element.closest("[data-inline-row]");
        return {name: state.select.name, index: row?.dataset.inlineRow ?? "", identity: row?.querySelector("[data-choice-row-id]")?.value ?? ""};
    };
    const contextKey = (state) => JSON.stringify(rowContext(state));
    const selectedItems = (state) => Array.from(state.select.selectedOptions).filter((option) => option.value !== "").map((option) => ({value: option.value, label: option.textContent}));
    const enabled = (state) => state.element.isConnected && !state.select.matches(":disabled") && !state.element.hasAttribute("data-choice-disabled");
    const buttons = (state) => {
        const unavailable = !enabled(state);
        state.query.disabled = unavailable;
        state.search.disabled = unavailable;
        state.previous.disabled = unavailable || state.busy || state.query.value !== state.term || state.offset === 0;
        state.next.disabled = unavailable || state.busy || state.query.value !== state.term || !state.more || state.offset + state.limit > 1000000;
        if (state.clear) state.clear.disabled = unavailable || selectedItems(state).length === 0;
    };
    const cancel = (state) => {
        state.sequence++;
        state.controller?.abort();
        state.controller = null;
        clearTimeout(state.timer);
        state.busy = false;
        state.element.removeAttribute("aria-busy");
        buttons(state);
    };
    const renderOptions = (state) => {
        const selected = new Map(selectedItems(state).map((item) => [item.value, item.label]));
        const page = new Map(state.page.map((item) => [item.value, item.label]));
        const displayed = new Map([...selected, ...page]);
        const options = document.createDocumentFragment();
        if (!state.select.multiple) {
            const empty = document.createElement("option");
            empty.value = "";
            empty.textContent = "---------";
            empty.selected = selected.size === 0;
            options.append(empty);
        }
        for (const [value, label] of displayed) {
            const option = document.createElement("option");
            option.value = value;
            option.textContent = label;
            option.selected = selected.has(value);
            if (page.has(value)) option.setAttribute("data-choice-page", "");
            options.append(option);
        }
        state.select.replaceChildren(options);
        buttons(state);
    };
    const describe = (state) => {
        const count = selectedItems(state).length;
        state.status.textContent = "Page " + (Math.floor(state.offset / state.limit) + 1) + ": " + state.page.length + " choices. " + count + " selected." + (state.more ? " More choices available." : "");
    };
    const validPage = (page, offset, limit) => {
        if (!page || page.offset !== offset || page.limit !== limit || typeof page.more !== "boolean" || !Array.isArray(page.items) || page.items.length > limit) return false;
        const seen = new Set();
        for (const item of page.items) {
            // Never convert relation identifiers to Number: int64 keys can be
            // zero, negative, or larger than JavaScript's exact integer range.
            if (!item || typeof item.value !== "string" || !/^-?(0|[1-9][0-9]*)$/.test(item.value) || item.value.length > 20 || typeof item.label !== "string" || encoder.encode(item.label).length > 4096 || seen.has(item.value)) return false;
            seen.add(item.value);
        }
        return true;
    };
    const search = async (state, offset) => {
        cancel(state);
        if (!enabled(state)) return;
        const term = state.query.value;
        if (encoder.encode(term).length > 256 || term.includes("\0")) {
            state.status.textContent = "Search text is too long or contains an unsupported character.";
            return;
        }
        if (!Number.isSafeInteger(offset) || offset < 0 || offset > 1000000) return;
        const row = rowContext(state);
        const url = new URL(state.element.dataset.choiceUrl, document.baseURI);
        if (url.origin !== window.location.origin) return;
        url.searchParams.set("q", term);
        url.searchParams.set("offset", String(offset));
        url.searchParams.set("limit", String(state.limit));
        if (row.index !== "") {
            if (integer(row.index) === null) return;
            url.searchParams.set("index", row.index);
            if (row.identity !== "") url.searchParams.set("row_id", row.identity);
        }
        const identity = contextKey(state);
        const sequence = state.sequence;
        const controller = new AbortController();
        state.controller = controller;
        state.busy = true;
        state.element.setAttribute("aria-busy", "true");
        state.status.textContent = "Loading choices…";
        buttons(state);
        try {
            const response = await fetch(url, {method: "GET", credentials: "same-origin", headers: {Accept: "application/json"}, cache: "no-store", signal: controller.signal});
            if (!response.ok) throw new Error("request failed");
            const page = await response.json();
            if (!validPage(page, offset, state.limit)) throw new Error("invalid response");
            if (sequence !== state.sequence || !enabled(state) || identity !== contextKey(state)) return;
            state.page = page.items;
            state.offset = page.offset;
            state.more = page.more;
            state.term = term;
            renderOptions(state);
            describe(state);
        } catch (error) {
            if (sequence === state.sequence && enabled(state) && identity === contextKey(state) && error.name !== "AbortError") {
                state.status.textContent = "Could not load choices. Your selection is unchanged. Try Search again.";
            }
        } finally {
            if (sequence === state.sequence) {
                state.busy = false;
                state.controller = null;
                state.element.removeAttribute("aria-busy");
                buttons(state);
            }
        }
    };
    const initialize = (root) => {
        const elements = [...(root.matches?.("[data-choice-url]") ? [root] : []), ...root.querySelectorAll("[data-choice-url]")];
        for (const element of elements) {
            if (states.has(element)) continue;
            const select = element.querySelector("select");
            const controls = element.querySelector("[data-choice-controls]");
            const query = element.querySelector("[data-choice-query]");
            const searchButton = element.querySelector("[data-choice-search]");
            const previous = element.querySelector("[data-choice-previous]");
            const next = element.querySelector("[data-choice-next]");
            const status = element.querySelector("[data-choice-status]");
            const limit = integer(element.dataset.choiceLimit);
            const offset = integer(element.dataset.choiceOffset);
            if (!select || !controls || !query || !searchButton || !previous || !next || !status || limit === null || limit < 1 || limit > 100 || offset === null || offset > 1000000 || !element.dataset.choiceUrl || element.hasAttribute("data-choice-disabled") || select.matches(":disabled")) continue;
            const state = {element, select, controls, query, search: searchButton, previous, next, status, clear: element.querySelector("[data-choice-clear]"), offset, limit, more: element.dataset.choiceMore === "true", term: "", page: Array.from(select.options).filter((option) => option.hasAttribute("data-choice-page")).map((option) => ({value: option.value, label: option.textContent})), sequence: 0, busy: false, controller: null, timer: null};
            state.context = contextKey(state);
            states.set(element, state);
            controls.hidden = false;
            buttons(state);
            describe(state);
            searchButton.addEventListener("click", () => search(state, 0));
            previous.addEventListener("click", () => search(state, Math.max(0, state.offset - state.limit)));
            next.addEventListener("click", () => search(state, state.offset + state.limit));
            query.addEventListener("input", () => {
                cancel(state);
                state.status.textContent = "Waiting to search…";
                state.timer = setTimeout(() => search(state, 0), 250);
            });
            query.addEventListener("keydown", (event) => {
                if (event.key === "Enter") {
                    event.preventDefault();
                    search(state, 0);
                }
            });
            select.addEventListener("change", () => {
                // Keep native keyboard selection and scroll position intact.
                // Only a deselected value outside the current page is removed.
                for (const option of Array.from(select.options)) {
                    if (option.value !== "" && !option.selected && !option.hasAttribute("data-choice-page")) option.remove();
                }
                buttons(state);
                if (!state.busy) describe(state);
            });
            state.clear?.addEventListener("click", () => {
                for (const option of select.options) option.selected = false;
                select.dispatchEvent(new Event("change", {bubbles: true}));
            });
        }
    };
    initialize(document);
    document.addEventListener("formset:added", (event) => {
        initialize(event.target);
        for (const state of states.values()) {
            if (event.target.contains(state.element)) search(state, 0);
        }
    });
    document.addEventListener("formset:removed", () => {
        for (const [element, state] of states) {
            if (!element.isConnected) {
                cancel(state);
                states.delete(element);
            } else if (state.context !== contextKey(state)) {
                state.context = contextKey(state);
                cancel(state);
                state.page = [];
                state.offset = 0;
                state.more = false;
                renderOptions(state);
                search(state, 0);
            }
        }
    });
})();
