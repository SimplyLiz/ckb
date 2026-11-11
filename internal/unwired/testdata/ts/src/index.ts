import { wired, Door, Opener } from './lib';
wired();
const door: Opener = new Door();
door.open();
