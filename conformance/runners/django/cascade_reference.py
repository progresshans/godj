"""Independent public Django 6.1 CASCADE observations; BSD-3-Clause reference.

Authored declarations and operations only; does not read GoDj or expected data.
This is a preparatory observation, not a GoDj implementation claim.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import sys
import tempfile
import types
from pathlib import Path

import django
from django.apps import AppConfig
from django.conf import settings


def observe():
    assert django.get_version() == '6.1' and not settings.configured
    database = os.environ.get('GODJ_CASCADE_DATABASE')
    if database:
        assert database.startswith('godj_cascade_')
        assert importlib.metadata.version('psycopg') == '3.3.6'
    with tempfile.TemporaryDirectory(prefix='godj-cascade-django-') as directory:
        installed = []
        for name in ('cascadeparents', 'cascadedetails'):
            module = types.ModuleType(name)
            module.__file__ = str(Path(directory) / (name + '.py'))
            module.__path__ = [directory]
            module.ReferenceConfig = type('ReferenceConfig', (AppConfig,), {
                'name': name, 'label': name, 'path': directory, '__module__': name,
            })
            sys.modules[name] = module
            installed.append(name + '.ReferenceConfig')
        config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(directory) / 'reference.sqlite3')}
        if database:
            config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
        settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=installed,
                           DATABASES={'default': config}, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField',
                           USE_TZ=True, TIME_ZONE='UTC', LANGUAGE_CODE='en-us')
        django.setup()
        from django.db import DatabaseError, IntegrityError, connection, models, transaction
        from django.db.models.deletion import Collector, ProtectedError

        class Label(models.Model):
            name = models.CharField(max_length=64)

            class Meta:
                app_label = 'cascadeparents'
                db_table = 'cascade_label'

        class Root(models.Model):
            name = models.CharField(max_length=64)
            labels = models.ManyToManyField(Label, related_name='roots')

            class Meta:
                app_label = 'cascadeparents'
                db_table = 'cascade_root'

        class Child(models.Model):
            root = models.ForeignKey(Root, on_delete=models.CASCADE, related_name='children')

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_child'

        class Grandchild(models.Model):
            child = models.ForeignKey(Child, on_delete=models.CASCADE, related_name='grandchildren')

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_grandchild'

        class Watcher(models.Model):
            child = models.ForeignKey(Child, null=True, on_delete=models.SET_NULL)

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_watcher'

        class Protected(models.Model):
            grandchild = models.ForeignKey(Grandchild, on_delete=models.PROTECT)

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_protected'

        class Twin(models.Model):
            first = models.ForeignKey(Root, on_delete=models.CASCADE, related_name='first_twins')
            second = models.ForeignKey(Root, on_delete=models.CASCADE, related_name='second_twins')

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_twin'

        class Hidden(models.Model):
            root = models.ForeignKey(Root, on_delete=models.CASCADE, related_name='+')

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_hidden'

        class Detail(models.Model):
            root = models.OneToOneField(Root, on_delete=models.CASCADE, related_name='detail')

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_detail'

        class Overlap(models.Model):
            cascade_root = models.ForeignKey(Root, on_delete=models.CASCADE, related_name='overlap_cascade')
            protected_root = models.ForeignKey(Root, on_delete=models.PROTECT, related_name='overlap_protect')

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_overlap'

        class Node(models.Model):
            parent = models.ForeignKey('self', null=True, on_delete=models.CASCADE, related_name='children')

            class Meta:
                app_label = 'cascadeparents'
                db_table = 'cascade_node'

        class Left(models.Model):
            right = models.ForeignKey('cascadedetails.Right', null=True, on_delete=models.CASCADE)

            class Meta:
                app_label = 'cascadeparents'
                db_table = 'cascade_left'

        class Right(models.Model):
            left = models.ForeignKey(Left, on_delete=models.CASCADE)

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_right'

        class RequiredLeft(models.Model):
            right = models.ForeignKey('cascadedetails.RequiredRight', on_delete=models.CASCADE)

            class Meta:
                app_label = 'cascadeparents'
                db_table = 'cascade_required_left'

        class RequiredRight(models.Model):
            left = models.ForeignKey(RequiredLeft, on_delete=models.CASCADE)

            class Meta:
                app_label = 'cascadedetails'
                db_table = 'cascade_required_right'

        declared = [Label, Root, Child, Grandchild, Watcher, Protected, Twin, Hidden, Detail, Overlap,
                    Node, Left, Right, RequiredLeft, RequiredRight]
        through = Root._meta.get_field('labels').remote_field.through
        stored = declared + [through]
        results = {}

        def snapshot():
            return {m._meta.label: list(m.objects.order_by('pk').values_list()) for m in stored}

        def counts():
            return {m._meta.label: count for m in stored if (count := m.objects.count())}

        def deleted(value):
            count, per_model = value
            return {'total': count, 'models': per_model}

        def clear():
            with transaction.atomic(), connection.cursor() as cursor:
                for m in reversed(stored):
                    cursor.execute('DELETE FROM ' + connection.ops.quote_name(m._meta.db_table))
            assert not counts()

        def tree():
            root = Root.objects.create(name='root')
            child = Child.objects.create(root=root)
            grandchild = Grandchild.objects.create(child=child)
            watcher = Watcher.objects.create(child=child)
            return root, child, grandchild, watcher

        def tracked(execute, sql, params, many, context):
            if sql.lstrip().upper().startswith(('UPDATE ', 'DELETE ', 'INSERT ')):
                writes.append(sql)
            return execute(sql, params, many, context)

        created = False
        try:
            with connection.schema_editor() as editor:
                for model in declared:
                    editor.create_model(model)
            created = True
            root, child, grandchild, watcher = tree()
            other = Root.objects.create(name='other')
            other_child = Child.objects.create(root=other)
            result = deleted(root.delete())
            result.update(root_pk_cleared=root.pk is None,
                          retained_child_instance_pk=child.pk is not None,
                          watcher_database_null=Watcher.objects.get(pk=watcher.pk).child_id is None,
                          watcher_held_value_preserved=watcher.child_id == child.pk,
                          unrelated_child_preserved=Child.objects.filter(pk=other_child.pk).exists(), counts=counts())
            results['recursive_set_null'] = result
            clear()

            root, child, grandchild, watcher = tree()
            Protected.objects.create(grandchild=grandchild)
            before, key, writes = snapshot(), root.pk, []
            with connection.execute_wrapper(tracked):
                try:
                    root.delete()
                    raise AssertionError('protected descendant was deleted')
                except ProtectedError as error:
                    protected = len(error.protected_objects)
            results['protected_descendant'] = {'protected_count': protected, 'writes': len(writes),
                                               'rows_preserved': snapshot() == before, 'caller_pk_preserved': root.pk == key}
            clear()

            root = Root.objects.create(name='overlap')
            Overlap.objects.create(cascade_root=root, protected_root=root)
            before = snapshot()
            try:
                root.delete()
                raise AssertionError('PROTECT was bypassed by a cascade path')
            except ProtectedError as error:
                results['protect_cascade_overlap'] = {'protected_count': len(error.protected_objects),
                                                       'rows_preserved': snapshot() == before}
            clear()

            root = Root.objects.create(name='twice')
            Twin.objects.create(first=root, second=root)
            results['duplicate_paths'] = deleted(root.delete())
            clear()

            root = Root.objects.create(name='hidden-and-one-to-one')
            Hidden.objects.create(root=root)
            Detail.objects.create(root=root)
            results['hidden_and_one_to_one'] = deleted(root.delete())
            clear()

            node = Node.objects.create()
            node.parent = node
            node.save(update_fields=['parent'])
            results['self_loop'] = deleted(node.delete())
            clear()

            first = Node.objects.create()
            second = Node.objects.create(parent=first)
            first.parent = second
            first.save(update_fields=['parent'])
            results['same_model_cycle'] = deleted(first.delete())
            clear()

            node = Node.objects.create()
            Node.objects.create(parent=node)
            unrelated = Node.objects.create()
            results['nullable_cascade'] = dict(deleted(node.delete()),
                                               unrelated_null_preserved=Node.objects.filter(pk=unrelated.pk).exists())
            clear()

            left = Left.objects.create()
            right = Right.objects.create(left=left)
            left.right = right
            left.save(update_fields=['right'])
            results['cross_app_cycle'] = deleted(left.delete())
            clear()

            with transaction.atomic():
                required_left = RequiredLeft.objects.create(id=101, right_id=202)
                RequiredRight.objects.create(id=202, left_id=101)
            results['required_cross_app_cycle'] = deleted(required_left.delete())
            clear()

            root, child, grandchild, watcher = tree()
            before, key, writes = snapshot(), root.pk, []
            def fail_root(execute, sql, params, many, context):
                if sql.lstrip().upper().startswith('DELETE FROM "CASCADE_ROOT"'):
                    raise DatabaseError('independent late delete failure')
                return tracked(execute, sql, params, many, context)
            try:
                with connection.execute_wrapper(fail_root):
                    root.delete()
                raise AssertionError('late failure injection was not reached')
            except DatabaseError:
                results['late_failure'] = {'prior_mutation_executed': bool(writes),
                                           'rows_preserved': snapshot() == before, 'caller_pk_preserved': root.pk == key}
            clear()

            root, child, grandchild, watcher = tree()
            before = snapshot()
            try:
                with transaction.atomic(), connection.cursor() as cursor:
                    cursor.execute('DELETE FROM "cascade_root" WHERE id = %s', [root.pk])
                raise AssertionError('raw delete bypassed native FK')
            except IntegrityError:
                results['raw_delete'] = {'integrity_error': True, 'rows_preserved': snapshot() == before}
            clear()

            label = Label.objects.create(name='shared')
            second_label = Label.objects.create(name='second')
            root = Root.objects.create(name='first')
            other = Root.objects.create(name='second')
            root.labels.add(label, second_label, label)
            other.labels.add(label)
            initial_links = through.objects.count()
            root_result = deleted(root.delete())
            after_root = {'labels': Label.objects.count(), 'links': through.objects.count(),
                          'other_members': list(other.labels.values_list('name', flat=True))}
            label_result = deleted(label.delete())
            results['many_to_many_cleanup'] = {'initial_unique_links': initial_links, 'root_delete': root_result,
                                               'after_root': after_root, 'label_delete': label_result,
                                               'other_root_preserved': Root.objects.filter(pk=other.pk).exists(),
                                               'remaining_links': through.objects.count(),
                                               'remaining_labels': list(Label.objects.values_list('name', flat=True))}
            clear()

            with connection.cursor() as cursor:
                cursor.execute('SHOW server_version_num' if database else 'SELECT sqlite_version()')
                version = cursor.fetchone()[0]
                if database:
                    cursor.execute("SELECT confdeltype, condeferrable, condeferred FROM pg_constraint WHERE conrelid = 'cascade_child'::regclass AND contype = 'f'")
                    physical = list(cursor.fetchall()[0])
                else:
                    cursor.execute('PRAGMA foreign_key_list(cascade_child)')
                    physical = {'on_delete': cursor.fetchall()[0][6]}
            fields = [f for f in through._meta.fields if f.is_relation]
            return {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
                    'database_version': version, 'observations': results, 'physical_fk': physical,
                    'automatic_through': {'unique_together': through._meta.unique_together,
                                           'policies': [f.remote_field.on_delete.__name__ for f in fields]},
                    'source_sha256': {name: hashlib.sha256(Path(inspect.getsourcefile(value)).read_bytes()).hexdigest()
                                      for name, value in [('Model', models.Model), ('Collector', Collector),
                                                          ('ForeignKey', models.ForeignKey), ('ManyToManyField', models.ManyToManyField)]}}
        finally:
            if created:
                clear()
                with connection.schema_editor() as editor:
                    for model in reversed(declared):
                        editor.delete_model(model)
                assert not connection.introspection.table_names()
            connection.close()


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, separators=(',', ':')))
