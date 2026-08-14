// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import styles from './header.module.scss';

type Props = {
    left: React.ReactNode;
    right?: React.ReactNode;
    className?: string;
};

// Shared 56px product header bar: a full-width row with a flexible left region
// and a shrink-to-fit right region for actions. Docs Home and the Space view
// share the chrome (height, border, padding); each supplies its own content.
const Header = ({left, right, className}: Props) => (
    <header className={classNames(styles.header, className)}>
        <div className={styles.left}>{left}</div>
        {right != null && <div className={styles.right}>{right}</div>}
    </header>
);

export default Header;
