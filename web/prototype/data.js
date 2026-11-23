// The fixed data of the prototype (packet FA2-T8 6.2) as Namespace objects, bundled into the
// pages. Object files are not here: the Inspector fetches them on demand.
import { Namespace } from './model.js';
import cluster from './data/cluster.json';

const answers = import.meta.glob('./data/ns-*.json', { eager: true, import: 'default' });

export { cluster };

export const NAMESPACES = new Map(
  Object.values(answers)
    .map((a) => new Namespace(a))
    .map((ns) => [ns.name, ns]),
);
