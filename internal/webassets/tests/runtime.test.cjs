const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const path = require('node:path');
const {test} = require('node:test');
const vm = require('node:vm');
const ts = require('typescript');

// Each test gets an isolated browser global and module cache. These tests exercise
// lifecycle logic only; protocol and DOM behavior are covered by browser E2E.
function runtimeEnvironment() {
    class TrackedTarget extends EventTarget {
        listeners = new Map();
        addEventListener(type, listener, options) {
            this.listeners.set(type, listener);
            super.addEventListener(type, listener, options);
        }
        removeEventListener(type, listener) {
            this.listeners.delete(type);
            super.removeEventListener(type, listener);
        }
    }
    const window = new TrackedTarget();
    const document = new TrackedTarget();
    document.readyState = 'loading';
    const timers = new Map();
    let timerID = 0;
    const context = vm.createContext({
        window, document,
        setTimeout: callback => { timers.set(++timerID, callback); return timerID; },
        clearTimeout: id => timers.delete(id),
    });
    const cache = new Map();
    function load(filename) {
        filename = path.resolve(filename);
        if (cache.has(filename)) return cache.get(filename);
        const output = ts.transpileModule(readFileSync(filename, 'utf8'), {
            compilerOptions: {module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022},
        }).outputText;
        const exports = {};
        cache.set(filename, exports);
        const run = vm.runInContext(`(function(require, exports) {${output}\n})`, context);
        run(name => load(path.resolve(path.dirname(filename), `${name}.ts`)), exports);
        return exports;
    }
    return {window, document, timers, load: name => load(path.join(__dirname, '../src', name))};
}

test('DOM readiness releases both listeners and the deadline', async () => {
    for (const event of ['DOMContentLoaded', 'load']) {
        const env = runtimeEnvironment();
        const ready = env.load('utils/dom.ts').domReady();
        (event === 'load' ? env.window : env.document).dispatchEvent(new Event(event));
        await ready;
        assert.equal(env.document.listeners.size, 0);
        assert.equal(env.window.listeners.size, 0);
        assert.equal(env.timers.size, 0);
    }
});

test('DOM readiness timeout settles and cleans up in both modes', async () => {
    for (const forceResolve of [true, false]) {
        const env = runtimeEnvironment();
        const ready = env.load('utils/dom.ts').domReady({timeout: 10, forceResolve});
        for (const [id, callback] of [...env.timers]) {
            env.timers.delete(id);
            callback();
        }
        if (forceResolve) await ready;
        else await assert.rejects(ready, /DOM ready timeout after 10ms/);
        assert.equal(env.document.listeners.size, 0);
        assert.equal(env.window.listeners.size, 0);
        assert.equal(env.timers.size, 0);
    }
});

test('concurrent runtime starts share one bootstrap and ready instance', async () => {
    const env = runtimeEnvironment();
    const {ensureRuntimeStarted} = env.load('runtime/lifecycle.ts');
    let creations = 0;
    let release;
    const bootstrap = new Promise(resolve => { release = resolve; });
    const create = generation => {
        creations++;
        return {
            ready: false,
            runtimeReady() { return this.ready; },
            setRuntimeReady() { this.ready = true; },
            isRuntimeGeneration(value) { return value === generation; },
            destroy() {},
        };
    };
    const first = ensureRuntimeStarted('ffi', create, () => bootstrap);
    const second = ensureRuntimeStarted('ffi', create, () => bootstrap);
    release();
    const instance = await first;
    assert.equal(await second, instance);
    assert.equal(await ensureRuntimeStarted('ffi', create, () => bootstrap), instance);
    assert.equal(creations, 1);
    assert.equal(instance.runtimeReady(), true);
    assert.equal(Object.keys(env.window).includes('__cdp_ffi'), false);
});

test('failed bootstrap cleans up and allows a successful retry', async () => {
    const env = runtimeEnvironment();
    const {ensureRuntimeStarted, markRuntimeDestroyed} = env.load('runtime/lifecycle.ts');
    let destroyed = 0;
    const instance = {
        runtimeReady: () => true,
        setRuntimeReady() {},
        isRuntimeGeneration: () => true,
        destroy() { destroyed++; markRuntimeDestroyed('ffi', this); },
    };
    assert.equal(await ensureRuntimeStarted('ffi', () => instance, async () => { throw new Error('bootstrap'); }), null);
    assert.equal(destroyed, 1);
    assert.equal(env.window.__cdp_ffi, undefined);
    assert.equal(await ensureRuntimeStarted('ffi', () => instance, async () => {}), instance);
});

test('clearing an old facade preserves its replacement', () => {
    const env = runtimeEnvironment();
    const {setCdpFFI, getCdpFFI, clearCdpFFI} = env.load('runtime/globals.ts');
    const old = {};
    const replacement = {};
    setCdpFFI(old);
    setCdpFFI(replacement);
    clearCdpFFI(old);
    assert.equal(getCdpFFI(), replacement);
    clearCdpFFI(replacement);
    assert.equal(getCdpFFI(), undefined);
});
