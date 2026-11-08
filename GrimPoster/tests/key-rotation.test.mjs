// Windows PowerShell and native Go update paths must trust exactly the same new key.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import crypto from 'node:crypto';

const go = fs.readFileSync('launcher-src/status.go', 'utf8');
const ps = fs.readFileSync('desktop/Check-GrimPosterUpdate.ps1', 'utf8');
const goKey = go.match(/const updatePublicKeyXML = `([^`]+)`/)?.[1];
const psKey = ps.match(/\$updatePublicKeyXml = '([^']+)'/)?.[1];
assert(goKey && psKey, 'Both clients must have a pinned public key');
assert.equal(goKey, psKey, 'The native and PowerShell verification keys must match');

const allowed = /^<RSAKeyValue><Modulus>([A-Za-z0-9+/=]+)<\/Modulus><Exponent>([A-Za-z0-9+/=]+)<\/Exponent><\/RSAKeyValue>$/;
const match = goKey.match(allowed);
assert(match, 'Embedded key must contain public RSA components only');
const key = crypto.createPublicKey({
  key: {
    kty: 'RSA',
    n: Buffer.from(match[1], 'base64').toString('base64url'),
    e: Buffer.from(match[2], 'base64').toString('base64url'),
  },
  format: 'jwk',
});
assert.equal(key.asymmetricKeyDetails.modulusLength, 3072);
const fingerprint = crypto.createHash('sha256')
  .update(key.export({type: 'spki', format: 'der'})).digest('hex');
assert.equal(fingerprint, 'acd13f829e228b48517737c12e180dd0beabb5138e5e7d41d3c3f7758ae47c2e');
console.log('Key rotation: Go and PowerShell verify with the same 3072-bit public-only RSA key.');
