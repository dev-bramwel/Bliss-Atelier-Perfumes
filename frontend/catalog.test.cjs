const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

test('database prices load, unsafe media and failures clear products, stale carts block', async () => {
  let response;
  const context = vm.createContext({
    document: {querySelector: () => null, dispatchEvent: () => {}},
    window: {addEventListener: () => {}},
    localStorage: {getItem: () => null, setItem: () => {}},
    setTimeout: () => {}, setInterval: () => {},
    AbortSignal, Event, Date,
    fetch: async () => response,
  });
  vm.runInContext(fs.readFileSync(__dirname + '/shared.js', 'utf8'), context);
  const product = {id: 'p1', name: 'A <perfume>', category: 'Woody', gender: 'Unisex', size: '100ml', notes: '', description: '', priceKes: 2500, available: true, images: ['img/arabian%20wood.jpeg']};
  response = {ok: true, json: async () => ({products: [product]})};
  await vm.runInContext('loadCatalog()', context);
  assert.equal(vm.runInContext('getProduct("p1").priceKes', context), 2500);
  assert.equal(vm.runInContext('getProduct("p1").name', context), 'A <perfume>');
  vm.runInContext('cart = {p1: 2}', context);
  assert.equal(vm.runInContext('cartTotal()', context), 5000);
  assert.equal(vm.runInContext('cartAvailable()', context), true);
  vm.runInContext('catalogLoadedAt = Date.now() - 61000', context);
  assert.equal(vm.runInContext('cartAvailable()', context), false);
  response = {ok: false};
  await vm.runInContext('loadCatalog()', context);
  assert.equal(vm.runInContext('PRODUCTS.length', context), 0);
  assert.equal(vm.runInContext('catalogState', context), 'unavailable');
  response = {ok: true, json: async () => ({products: [{...product, images: ['javascript:alert(1)']}]})};
  await vm.runInContext('loadCatalog()', context);
  assert.equal(vm.runInContext('PRODUCTS.length', context), 0);
});
