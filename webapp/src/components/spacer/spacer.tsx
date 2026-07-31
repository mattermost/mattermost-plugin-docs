// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import styles from './spacer.module.scss';

/**
 * A flexible gap in a flex row or column: absorbs the free space so the siblings
 * after it sit at the far end. Preferred over an `auto` margin on the trailing
 * element, because the split is then visible in the markup rather than implied by
 * a style rule.
 */
const Spacer = () => (
    <div
        className={styles.spacer}
        role='presentation'
    />
);

export default Spacer;
