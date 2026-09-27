"""Independent pinned Django self-service password change (BSD-3-Clause).

Only native Django and private databases produce observations. This runner
does not import GoDj or read expected output fixtures.
"""
import hashlib
import os
import re
import importlib.metadata
import inspect
import json
import platform
import sys
import tempfile
import types
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import patch
import django
from django.conf import settings

assert django.get_version() == '6.1' and not settings.configured
with tempfile.TemporaryDirectory(prefix='godj-self-password-reference-') as directory:
    name=os.environ.get('GODJ_SELF_PASSWORD_DATABASE')
    if name:
        assert re.fullmatch(r'godj_self_password_[0-9]+',name)
        assert importlib.metadata.version('psycopg')=='3.3.6'
    database={'ENGINE':'django.db.backends.sqlite3','NAME':str(Path(directory)/'reference.sqlite3')}
    if name:
        database={'ENGINE':'django.db.backends.postgresql','NAME':name,'HOST':os.environ['PGHOST'],'PORT':os.environ['PGPORT'],'USER':os.environ['PGUSER'],'PASSWORD':os.environ['PGPASSWORD']}
    settings.configure(
        SECRET_KEY='independent-self-password-reference-only', USE_TZ=True,
        INSTALLED_APPS=['django.contrib.auth', 'django.contrib.contenttypes', 'django.contrib.sessions'],
        DATABASES={'default':database},
        DEFAULT_AUTO_FIELD='django.db.models.BigAutoField', ROOT_URLCONF='self_password_reference_urls', ALLOWED_HOSTS=['testserver'],
        MIDDLEWARE=['django.contrib.sessions.middleware.SessionMiddleware', 'django.contrib.auth.middleware.AuthenticationMiddleware'],
        PASSWORD_HASHERS=['django.contrib.auth.hashers.PBKDF2PasswordHasher'],
        AUTH_PASSWORD_VALIDATORS=[{'NAME':'django.contrib.auth.password_validation.MinimumLengthValidator','OPTIONS':{'min_length':8}}],
        LOGGING={'version':1,'disable_existing_loggers':False,'handlers':{'quiet':{'class':'logging.NullHandler'}},'loggers':{'django.request':{'handlers':['quiet'],'propagate':False}}},
    )
    django.setup()
    from django.contrib import auth
    from django.contrib.auth import models, forms, base_user, backends, hashers, views
    from django.contrib.auth.models import User, Group, Permission
    from django.contrib.contenttypes.models import ContentType
    from django.contrib.sessions.models import Session
    from django.contrib.sessions.backends import db as session_backend
    from django.contrib.sessions import middleware as session_middleware
    from django.core.management import call_command
    from django.db import connection, connections
    from django.db.migrations.recorder import MigrationRecorder
    from django.http import JsonResponse
    from django.test import Client
    from django.urls import path

    before_save = lambda form: None
    def login(request):
        user = auth.authenticate(request, username=request.POST.get('username'), password=request.POST.get('password'))
        if user is None:
            return JsonResponse({}, status=401)
        auth.login(request, user)
        request.session['payload'] = 'preserved private data'
        return JsonResponse({}, status=200)
    def who(request):
        return JsonResponse({'authenticated':request.user.is_authenticated, 'payload':request.session.get('payload')})
    class ChangeView(views.PasswordChangeView):
        success_url = '/done/'
        def form_invalid(self, form):
            # Preserve the native invalid-form 200 while replacing template
            # rendering with structured error observations.
            return JsonResponse({'errors':{key:[error.code for error in values] for key,values in form.errors.as_data().items()}})
        def form_valid(self, form):
            before_save(form)
            return super().form_valid(form)
    urls = types.ModuleType('self_password_reference_urls')
    urls.urlpatterns = [path('login/',login),path('who/',who),path('change/',ChangeView.as_view())]
    sys.modules[urls.__name__] = urls
    assert not connection.introspection.table_names()
    call_command('migrate',verbosity=0,interactive=False)
    base = datetime(2026,9,27,1,2,3,123456,tzinfo=timezone.utc)
    old_password,new_password = '  original private password  ','  replacement private password  '
    def seed(name):
        user=User.objects.create_user(username=name,password=old_password,email='member@example.test')
        client=Client(raise_request_exception=False)
        with patch('django.utils.timezone.now',return_value=base):
            assert client.post('/login/',{'username':name,'password':old_password}).status_code==200
        return user,client
    def post(client,old=old_password,new=new_password,confirm=new_password):
        return client.post('/change/',{'old_password':old,'new_password1':new,'new_password2':confirm})
    def last_login(user):
        value=User.objects.get(pk=user.pk).last_login
        return value.isoformat() if value else None
    try:
        observations={}
        user,client=seed('ordinary-member')
        other=Client()
        with patch('django.utils.timezone.now',return_value=base):
            assert other.post('/login/',{'username':user.username,'password':old_password}).status_code==200
        original_key=client.session.session_key
        other_key=other.session.session_key
        before=User.objects.get(pk=user.pk)
        refusals={}
        for mode,old,new,confirmation in [
            ('wrong_old','wrong',new_password,new_password),
            ('stripped_old',old_password.strip(),new_password,new_password),
            ('mismatch',old_password,new_password,new_password.strip()),
            ('weak',old_password,'short','short'),
            ('wrong_old_weak','wrong','short','short'),
            ('wrong_old_mismatch','wrong',new_password,'different'),
            ('missing_old_weak','','short','short'),
            ('missing_first_weak',old_password,'','short'),
            ('missing_confirmation',old_password,new_password,''),
            ('all_missing','','',''),
        ]:
            response=post(client,old,new,confirmation)
            current=User.objects.get(pk=user.pk)
            refusals[mode]={'status':response.status_code,'errors':response.json()['errors'],
                            'password_unchanged':current.password==before.password,'session_unchanged':client.session.session_key==original_key,
                            'last_login':last_login(user),'new_cookie':settings.SESSION_COOKIE_NAME in response.cookies}
        observations['refusals']=refusals
        with patch('django.utils.timezone.now',return_value=base+timedelta(seconds=1)):
            response=post(client)
            current=User.objects.get(pk=user.pk)
            observations['success']={'status':response.status_code,'session_rotated':client.session.session_key!=original_key,
                'old_session_retained':Session.objects.filter(session_key=original_key).exists(),
                'other_session_before_access':Session.objects.filter(session_key=other_key).exists(),
                'current':client.get('/who/').json(),'other':other.get('/who/').json(),
                'other_session_after_access':Session.objects.filter(session_key=other_key).exists(),
                'old_password_valid':current.check_password(old_password),'new_password_valid':current.check_password(new_password),
                'trimmed_password_valid':current.check_password(new_password.strip()),'last_login':last_login(user),
                'active':current.is_active,'staff':current.is_staff,'superuser':current.is_superuser}
        observations['same_password']={}
        with patch('django.utils.timezone.now',return_value=base+timedelta(seconds=2)):
            before=User.objects.get(pk=user.pk)
            key=client.session.session_key
            response=post(client,new_password,new_password,new_password)
            after=User.objects.get(pk=user.pk)
            observations['same_password']={'status':response.status_code,'hash_changed':before.password!=after.password,
                'key_changed':client.session.session_key!=key,'current':client.get('/who/').json(),'last_login':last_login(user)}
        failures={}
        for mode in ('password_write','session_cycle','session_final_save'):
            subject,failed=seed('failure-'+mode)
            key=failed.session.session_key
            before=User.objects.get(pk=subject.pk)
            fault_hits = [0]
            save_user,save_session=User.save,session_backend.SessionStore.save
            def user_save(self,*args,**kwargs):
                if mode=='password_write' and self.pk==subject.pk:
                    fault_hits[0] += 1
                    raise RuntimeError('synthetic password update failure')
                return save_user(self,*args,**kwargs)
            def session_save(self,*args,**kwargs):
                if mode=='session_cycle':
                    fault_hits[0] += 1
                    raise RuntimeError('synthetic cycle failure')
                if mode=='session_final_save' and self.get(auth.HASH_SESSION_KEY)==User.objects.get(pk=subject.pk).get_session_auth_hash():
                    fault_hits[0] += 1
                    raise RuntimeError('synthetic final session save failure')
                return save_session(self,*args,**kwargs)
            with patch('django.utils.timezone.now',return_value=base+timedelta(seconds=3)),patch.object(User,'save',user_save),patch.object(session_backend.SessionStore,'save',session_save):
                response=post(failed)
            assert fault_hits[0] == 1, (mode, fault_hits)
            current=User.objects.get(pk=subject.pk)
            failures[mode]={'status':response.status_code,'password_changed':before.password!=current.password,
                'new_password_valid':current.check_password(new_password),'last_login':last_login(subject),
                'old_session_retained':Session.objects.filter(session_key=key).exists(),'new_cookie':settings.SESSION_COOKIE_NAME in response.cookies}
        observations['failures']=failures
        races={}
        for mode in ('password_changed','inactive'):
            subject,racing=seed('race-'+mode)
            def mutate(form):
                if mode=='password_changed':
                    User.objects.filter(pk=subject.pk).update(password=hashers.make_password('concurrent private replacement'))
                else:
                    User.objects.filter(pk=subject.pk).update(is_active=False)
            before_save=mutate
            with patch('django.utils.timezone.now',return_value=base+timedelta(seconds=4)):
                response=post(racing)
            current=User.objects.get(pk=subject.pk)
            races[mode]={'status':response.status_code,'new_password_valid':current.check_password(new_password),
                'concurrent_password_valid':current.check_password('concurrent private replacement'),'active':current.is_active,
                'next':racing.get('/who/').json(),'last_login':last_login(subject)}
        observations['post_validation_change']=races
        modules={'auth':auth,'models':models,'forms':forms,'base_user':base_user,'backends':backends,'hashers':hashers,
                 'session_backend':session_backend,'session_middleware':session_middleware,'views':views}
        result={'django':django.get_version(),'python':platform.python_version(),'database_version':str(connection.pg_version) if name else connection.Database.sqlite_version,
                'backend':connection.vendor,'source_sha256':{name:hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for name,module in modules.items()},'observations':observations}
    finally:
        with connection.schema_editor() as editor:
            for model in (User,Group,Permission,ContentType,Session,MigrationRecorder.Migration):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
print(json.dumps(result,sort_keys=True,indent=2))
