import { Callout } from '../components/Callout';

export const callout = {
  render: 'Callout',
  attributes: {
    type: {
      type: String,
      default: 'note',
      matches: ['note', 'warning', 'tip'],
    },
  },
};
